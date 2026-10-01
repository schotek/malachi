// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"time"
	"unicode/utf8"
)

// The worker is the bridge's own executable started again for one
// document: Run starts it with WorkerArg, ProtocolVersion and the format
// as its arguments, and the bridge's main hands those arguments to
// ServeWorker before it does anything else. The worker reads the document
// from stdin and writes one reply to stdout:
//
//	{"v":1,"refusal":"","what":"","textBytes":N,"facts":{…}}\n
//	N bytes of UTF-8 text
//
// and nothing after them. A refusal has a code (and maybe a detail), no
// text and no facts. The reply is the worker's only output: stderr is
// discarded unread (a library's panic could quote the document), and
// everything in the reply is checked against the protocol's closed sets
// and bounds, since a document built to attack a reader may have taken
// the worker over. The exit status tells how the worker ended when it did
// not reply.

// WorkerArg, as the first argument, makes the bridge's executable a
// worker. It is internal and not listed in the usage.
const WorkerArg = "__extract"

// ProtocolVersion is the version of the worker protocol, the argument
// after WorkerArg. A worker speaking another one (the bridge's binary was
// replaced while the bridge ran) exits with exitMismatch without reading
// anything.
const ProtocolVersion = 1

// Exit statuses of the worker.
const (
	exitOK       = 0 // a reply was written
	exitCrash    = 2 // Go's own status for a fatal error or an unrecovered panic; also stdin or stdout failing
	exitMemory   = 3 // the memory watchdog
	exitDeadline = 4 // the worker's own deadline, or its reader's context ended at workerReaderDeadline
	exitMismatch = 5 // bad arguments or another protocol version, or run as root
	exitEngine   = 6 // the reader's engine could not be started (errEngineStart); no reply
)

// The worker's guards. They are plain Go, the same on every platform: no
// rlimits, no job objects.
const (
	// workerProcs bounds the threads running Go at once (fewer when the
	// system gives the process fewer). Four, because a PDF worker
	// compiles PDFium with four workers first: measured on an Apple M4,
	// the cold start of a worker is 0.48 s with four and 0.75 s with two.
	workerProcs    = 4
	workerMaxStack = 64 << 20
	// workerSoftMemory is debug.SetMemoryLimit: above it the GC works
	// harder. A PDF worker holds at most PDFium's memory ceiling and one
	// growth copy (pdfMemoryPages, pdfMemoryEager: 320 MiB) beside a small
	// Go heap; PDFium's compile peaks near 300 MiB before that.
	workerSoftMemory = 512 << 20
	workerHardMemory = 1 << 30 // above it the watchdog ends the worker
	workerWatchEvery = 20 * time.Millisecond
	// workerDeadline is past the parent's own timeout, so the parent's
	// kill normally comes first; it ends a worker whose parent is gone.
	workerDeadline = 35 * time.Second
	// workerReaderDeadline is the deadline of the reader's context, before
	// the parent's 30 s timeout: a reader that honours its context (the
	// PDF engine stops PDFium when it ends) returns, and the worker exits
	// with exitDeadline rather than being killed.
	workerReaderDeadline = 28 * time.Second
)

// Reply framing.
const (
	maxHeaderBytes = 4 << 10 // the header line, newline included
	// replySlack is read beyond the largest valid reply, so that bytes
	// after the text show as such.
	replySlack = 64
)

// Outcome is how one Run ended.
type Outcome int

// The outcomes. Only OK carries text, only Refused a *Refusal.
const (
	OK           Outcome = iota // the text is in the Result
	Refused                     // the worker refused the document; the *Refusal says why
	Timeout                     // the parent's deadline killed the worker, or its own deadline ended it
	Memory                      // the memory watchdog ended the worker
	Crashed                     // the worker died: a Go fatal error, an unrecovered panic, a signal
	BadReply                    // the reply broke the protocol; as bad as a crash
	SpawnFailed                 // the worker could not be started
	Cancelled                   // ctx was cancelled (not timed out) before the worker ended
	Mismatch                    // the worker speaks another protocol: the binary was replaced while the bridge ran
	EngineFailed                // the reader's engine could not be started: the machine's doing, not the document's
)

// String is a short code for logs.
func (o Outcome) String() string {
	switch o {
	case OK:
		return "ok"
	case Refused:
		return "refused"
	case Timeout:
		return "timeout"
	case Memory:
		return "memory"
	case Crashed:
		return "crashed"
	case BadReply:
		return "badReply"
	case SpawnFailed:
		return "spawnFailed"
	case Cancelled:
		return "cancelled"
	case Mismatch:
		return "mismatch"
	case EngineFailed:
		return "engineFailed"
	}
	return "Outcome(" + strconv.Itoa(int(o)) + ")"
}

// replyHeader is the first line of a reply.
type replyHeader struct {
	V         int    `json:"v"`
	Refusal   Code   `json:"refusal"`
	What      string `json:"what"`
	TextBytes int    `json:"textBytes"`
	Facts     Facts  `json:"facts"`
}

// --- the worker --------------------------------------------------------------

// workerOptions are what serve applies around one extraction. The real
// worker (workerProcess) has every process-level guard on; a test that runs
// serve inside its own process turns them off and may swap the reader.
type workerOptions struct {
	// guards applies the root check, GOMAXPROCS, the stack and memory
	// limits, the watchdog and the deadline to the whole process.
	guards   bool
	version  int           // the protocol version the worker speaks
	deadline time.Duration // the worker's own deadline, with guards
	memory   uint64        // the watchdog's limit, with guards
	// readerDeadline is the deadline of the reader's context; 0 for none.
	readerDeadline time.Duration
	limits         Limits
	extract        func(context.Context, Format, []byte, Limits) (Result, error)
}

// workerProcess is the real worker's options.
func workerProcess() workerOptions {
	return workerOptions{
		guards:         true,
		version:        ProtocolVersion,
		deadline:       workerDeadline,
		memory:         workerHardMemory,
		readerDeadline: workerReaderDeadline,
		limits:         DefaultLimits(),
		extract:        Extract,
	}
}

// ServeWorker is the worker: args are the arguments after WorkerArg, stdin
// the document, stdout the reply. It returns the exit status. Call it only
// in a process of its own, and exit with its status right after: it
// changes process-wide settings and may end the process itself (memory
// watchdog, deadline).
func ServeWorker(args []string, stdin io.Reader, stdout io.Writer) int {
	return serve(args, stdin, stdout, workerProcess())
}

func serve(args []string, stdin io.Reader, stdout io.Writer, o workerOptions) int {
	if len(args) != 2 || args[0] != strconv.Itoa(o.version) || !Format(args[1]).Valid() {
		return exitMismatch
	}
	f := Format(args[1])
	if o.guards {
		// Like the bridge: a per-user tool has no business running as
		// root. Geteuid is -1 on Windows.
		if os.Geteuid() == 0 {
			return exitMismatch
		}
		guard(o)
	}
	data, err := io.ReadAll(io.LimitReader(stdin, int64(o.limits.MaxInputBytes)+1))
	if err != nil {
		return exitCrash
	}
	res, ref, noReply := extractSafely(f, data, o)
	if noReply != exitOK {
		// No reply: a timeout or an engine that did not start is no
		// verdict on the document, which a reply would be.
		return noReply
	}
	if err := writeReply(stdout, res, ref); err != nil {
		return exitCrash
	}
	return exitOK
}

// guard applies the process-level guards of the real worker.
func guard(o workerOptions) {
	runtime.GOMAXPROCS(min(workerProcs, runtime.GOMAXPROCS(0)))
	debug.SetMaxStack(workerMaxStack)
	debug.SetMemoryLimit(workerSoftMemory)
	go watchMemory(o.memory, workerWatchEvery, os.Exit)
	time.AfterFunc(o.deadline, func() { os.Exit(exitDeadline) })
}

// watchMemory ends the process with exitMemory once the memory the Go
// runtime holds from the system, less what it has given back, is over
// limit. A reader that allocates past the soft limit makes the GC work
// harder but is not stopped by it; this stops it.
func watchMemory(limit uint64, every time.Duration, exit func(int)) {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for range tick.C {
		metrics.Read(samples)
		if samples[0].Value.Kind() != metrics.KindUint64 || samples[1].Value.Kind() != metrics.KindUint64 {
			// A runtime without these metrics: the soft limit, the
			// parent's timeout and the system's own limits remain.
			return
		}
		if total, released := samples[0].Value.Uint64(), samples[1].Value.Uint64(); total > released && total-released > limit {
			exit(exitMemory)
			return
		}
	}
}

// extractSafely runs the reader on the document read from stdin and turns
// whatever it does short of a fatal error into a reply: a panic becomes
// readerFailed, and so does a result the protocol would not carry.
// noReply is the exit status of a worker that gives no reply, exitOK
// otherwise: exitDeadline for a reader that failed once its context's
// deadline had passed, exitEngine for one whose engine could not be
// started (errEngineStart).
func extractSafely(f Format, data []byte, o workerOptions) (res Result, ref *Refusal, noReply int) {
	switch {
	case len(data) > o.limits.MaxInputBytes:
		return Result{}, refuse(TooBig), exitOK
	case len(data) == 0:
		return Result{}, refuse(Damaged), exitOK
	}
	defer func() {
		if recover() != nil {
			res, ref, noReply = Result{}, refuse(ReaderFailed), exitOK
		}
	}()
	// A reader that ignores its context is ended by the worker's own
	// deadline or the parent's kill.
	ctx := context.Background()
	if o.readerDeadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.readerDeadline)
		defer cancel()
	}
	r, err := o.extract(ctx, f, data, o.limits)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, nil, exitDeadline
		}
		if errors.Is(err, errEngineStart) {
			return Result{}, nil, exitEngine
		}
		var refusal *Refusal
		if errors.As(err, &refusal) && refusal.Valid() {
			return Result{}, refusal, exitOK
		}
		return Result{}, refuse(ReaderFailed), exitOK
	}
	if checkResult(r, o.limits) != nil {
		return Result{}, refuse(ReaderFailed), exitOK
	}
	return r, nil, exitOK
}

// checkResult holds a result to what a reply may carry.
func checkResult(res Result, lim Limits) error {
	if len(res.Text) > lim.MaxTextBytes {
		return fmt.Errorf("text of %d bytes, limit %d", len(res.Text), lim.MaxTextBytes)
	}
	if !utf8.ValidString(res.Text) {
		return errors.New("text is not valid UTF-8")
	}
	return res.Facts.Validate()
}

// writeReply writes the reply: the header line, then the text. A refusal
// carries neither text nor facts.
func writeReply(w io.Writer, res Result, ref *Refusal) error {
	h := replyHeader{V: ProtocolVersion}
	if ref != nil {
		h.Refusal, h.What = ref.Code, ref.What
		res = Result{}
	} else {
		h.TextBytes, h.Facts = len(res.Text), res.Facts
	}
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(h); err != nil {
		return fmt.Errorf("extract: write reply header: %w", err)
	}
	if _, err := bw.WriteString(res.Text); err != nil {
		return fmt.Errorf("extract: write reply text: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("extract: write reply: %w", err)
	}
	return nil
}

// --- the parent --------------------------------------------------------------

// Run extracts the text of data in a worker: exe started with WorkerArg,
// ProtocolVersion and f (exe is the bridge's own executable, an absolute
// path from os.Executable). ctx bounds the worker's whole life: when it
// ends, the worker is killed. It returns the text with OK, a *Refusal with
// Refused, and otherwise how the worker failed.
//
// The worker applies DefaultLimits(); lim bounds what is sent (more than
// MaxInputBytes is refused without a worker) and what a reply may carry
// (MaxTextBytes), so production passes DefaultLimits() too.
//
// Run reaps the worker before it returns, whatever happened.
func Run(ctx context.Context, exe string, f Format, data []byte, lim Limits) (Result, *Refusal, Outcome) {
	res, ref, out, _ := run(ctx, exe, f, data, lim)
	return res, ref, out
}

// run is Run, also returning the state of the reaped worker (nil when none
// was started), so that a test can see it was reaped.
func run(ctx context.Context, exe string, f Format, data []byte, lim Limits) (Result, *Refusal, Outcome, *os.ProcessState) {
	switch {
	case !f.Valid():
		return Result{}, refuse(Unsupported), Refused, nil
	case len(data) > lim.MaxInputBytes:
		return Result{}, refuse(TooBig), Refused, nil
	case ctx.Err() != nil:
		return Result{}, nil, ended(ctx), nil
	}

	cmd := exec.CommandContext(ctx, exe, WorkerArg, strconv.Itoa(ProtocolVersion), string(f))
	// Discarded unread: a library's panic message could quote the
	// document, and nothing from the worker reaches a log.
	cmd.Stderr = nil
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, nil, SpawnFailed, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return Result{}, nil, SpawnFailed, nil
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return Result{}, nil, ended(ctx), nil
		}
		return Result{}, nil, SpawnFailed, nil
	}

	// The document goes in from a goroutine of its own, so that a worker
	// that replies before it has read everything (a refusal at the input
	// cap) cannot block the read of its reply. The write fails once the
	// worker has gone, which ends the goroutine.
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = stdin.Write(data)
		_ = stdin.Close()
	}()

	// One byte more than the largest valid reply (plus the slack) is read
	// at most: a worker writing more than that is killed rather than read
	// to its end.
	readCap := maxHeaderBytes + lim.MaxTextBytes + replySlack
	reply, readErr := io.ReadAll(io.LimitReader(stdout, int64(readCap)))
	flooded := len(reply) >= readCap
	if flooded || readErr != nil {
		_ = cmd.Process.Kill()
	}
	// The exit status is read from ProcessState; Wait's error says the
	// same in less detail.
	_ = cmd.Wait()
	<-wrote

	state := cmd.ProcessState
	switch {
	case state == nil:
		return Result{}, nil, Crashed, nil
	case flooded || readErr != nil:
		return Result{}, nil, BadReply, state
	case state.Success():
		res, ref, err := parseReply(reply, lim)
		switch {
		case err != nil:
			return Result{}, nil, BadReply, state
		case ref != nil:
			return Result{}, ref, Refused, state
		}
		return res, nil, OK, state
	case ctx.Err() != nil:
		return Result{}, nil, ended(ctx), state
	}
	switch state.ExitCode() {
	case exitMemory:
		return Result{}, nil, Memory, state
	case exitDeadline:
		return Result{}, nil, Timeout, state
	case exitMismatch:
		return Result{}, nil, Mismatch, state
	case exitEngine:
		// Said by the status alone: a worker that wrote anything as well
		// broke the protocol.
		if len(reply) > 0 {
			return Result{}, nil, BadReply, state
		}
		return Result{}, nil, EngineFailed, state
	}
	// exitCrash, a signal (-1), anything else.
	return Result{}, nil, Crashed, state
}

// ended is the outcome of a worker whose ctx ended.
func ended(ctx context.Context) Outcome {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Timeout
	}
	return Cancelled
}

// parseReply checks a whole reply strictly and returns its text or its
// refusal. Any deviation is an error: the header line within
// maxHeaderBytes, only the known fields, the protocol version, a refusal
// code and detail from their closed sets with no text and no facts, facts
// within their bounds, at most lim.MaxTextBytes of valid UTF-8 text, and
// exactly as many bytes as the header says, then the end.
func parseReply(reply []byte, lim Limits) (Result, *Refusal, error) {
	nl := bytes.IndexByte(reply[:min(len(reply), maxHeaderBytes)], '\n')
	if nl < 0 {
		return Result{}, nil, fmt.Errorf("no header line within %d bytes", maxHeaderBytes)
	}
	line, text := reply[:nl+1], reply[nl+1:]
	var h replyHeader
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Result{}, nil, fmt.Errorf("decode header: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, nil, errors.New("more than one value in the header line")
	}
	if h.V != ProtocolVersion {
		return Result{}, nil, fmt.Errorf("protocol version %d, want %d", h.V, ProtocolVersion)
	}
	if err := h.Facts.Validate(); err != nil {
		return Result{}, nil, err
	}
	if h.TextBytes < 0 || h.TextBytes > lim.MaxTextBytes {
		return Result{}, nil, fmt.Errorf("textBytes %d out of range", h.TextBytes)
	}
	if len(text) != h.TextBytes {
		return Result{}, nil, fmt.Errorf("%d bytes after the header, textBytes says %d", len(text), h.TextBytes)
	}
	if h.Refusal != "" {
		ref := &Refusal{Code: h.Refusal, What: h.What}
		switch {
		case !ref.Valid():
			return Result{}, nil, errors.New("refusal outside the closed sets")
		case h.TextBytes != 0 || h.Facts != (Facts{}):
			return Result{}, nil, errors.New("refusal with text or facts")
		}
		return Result{}, ref, nil
	}
	if h.What != WhatNone {
		return Result{}, nil, errors.New("a detail without a refusal")
	}
	if !utf8.Valid(text) {
		return Result{}, nil, errors.New("text is not valid UTF-8")
	}
	return Result{Text: string(text), Facts: h.Facts}, nil, nil
}
