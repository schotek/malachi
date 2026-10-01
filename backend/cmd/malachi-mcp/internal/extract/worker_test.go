// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inProcess is serve's options for a test inside the test process: no
// process-level guards, the reader swapped.
func inProcess(lim Limits, extract func(context.Context, Format, []byte, Limits) (Result, error)) workerOptions {
	return workerOptions{version: ProtocolVersion, limits: lim, extract: extract}
}

// serveBytes runs serve on input and returns its status and output.
func serveBytes(t *testing.T, args []string, input []byte, o workerOptions) (int, []byte) {
	t.Helper()
	var out bytes.Buffer
	code := serve(args, bytes.NewReader(input), &out, o)
	return code, out.Bytes()
}

// fullFacts sets every fact, to see each one cross the protocol.
func fullFacts() Facts {
	return Facts{
		Pages: 1, PagesRead: 2, PagesWithoutText: 3, PagesUndecodable: 4, PagesCut: 5,
		Sheets: 6, SheetsHidden: 7, SheetsRead: 8, SheetsSkipped: 9, Rows: 10,
		ColumnsDropped: 11, Formulas: 12, Uncalculated: 13, CellsUnresolved: 14,
		Comments: 15, Footnotes: 16, Endnotes: 17, HeadersFooters: 18,
		TrackedChanges: true, HiddenContent: true,
		Cut: true, CutAt: CutRows, CutPage: 19, CutSheet: 20, CutRow: MaxFact,
	}
}

func TestServeReplyRoundTrip(t *testing.T) {
	want := Result{Text: "<b>Q3</b> & more\n\tzweite Zeile: \u00fc\u4e2d", Facts: fullFacts()}
	o := inProcess(DefaultLimits(), func(_ context.Context, f Format, data []byte, _ Limits) (Result, error) {
		if f != DOCX || string(data) != "document" {
			return Result{}, fmt.Errorf("reader got %s %q", f, data)
		}
		return want, nil
	})
	code, out := serveBytes(t, []string{"1", "docx"}, []byte("document"), o)
	if code != exitOK {
		t.Fatalf("status %d", code)
	}
	header, _, _ := bytes.Cut(out, []byte("\n"))
	if !bytes.HasPrefix(header, []byte(`{"v":1,"refusal":"","what":"","textBytes":`)) {
		t.Errorf("header %s", header)
	}
	got, ref, err := parseReply(out, DefaultLimits())
	if err != nil || ref != nil {
		t.Fatalf("parseReply: %v, %v", ref, err)
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestServeRefusalReply(t *testing.T) {
	o := inProcess(DefaultLimits(), func(context.Context, Format, []byte, Limits) (Result, error) {
		return Result{Text: "dropped", Facts: Facts{Pages: 3}}, fmt.Errorf("open: %w", &Refusal{Code: Encrypted, What: WhatPassword})
	})
	code, out := serveBytes(t, []string{"1", "pdf"}, []byte("%PDF-1.7"), o)
	if code != exitOK {
		t.Fatalf("status %d", code)
	}
	if !strings.HasPrefix(string(out), `{"v":1,"refusal":"encrypted","what":"password","textBytes":0,"facts":{"pages":0,`) {
		t.Errorf("reply %s", out)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) {
		t.Errorf("a refusal carries text: %q", out)
	}
	_, ref, err := parseReply(out, DefaultLimits())
	if err != nil || ref == nil || *ref != (Refusal{Code: Encrypted, What: WhatPassword}) {
		t.Errorf("parseReply: %v, %v", ref, err)
	}
}

func TestServeArgs(t *testing.T) {
	called := false
	o := inProcess(DefaultLimits(), func(context.Context, Format, []byte, Limits) (Result, error) {
		called = true
		return Result{}, nil
	})
	for _, args := range [][]string{
		nil,
		{"1"},
		{"1", "pdf", "extra"},
		{"2", "pdf"},
		{"0", "pdf"},
		{"01", "pdf"},
		{"1", "doc"},
		{"1", "PDF"},
		{"1", ""},
	} {
		code, out := serveBytes(t, args, []byte("%PDF-1.7"), o)
		if code != exitMismatch || len(out) != 0 {
			t.Errorf("%q: status %d, output %q", args, code, out)
		}
	}
	if called {
		t.Error("the reader ran on bad arguments")
	}
}

func TestServeInputBounds(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxInputBytes = 8
	var got []byte
	o := inProcess(lim, func(_ context.Context, _ Format, data []byte, _ Limits) (Result, error) {
		got = data
		return Result{Text: "ok"}, nil
	})
	for _, c := range []struct {
		input  string
		refuse Code
	}{
		{"", Damaged},
		{"123456789", TooBig},
		{"123456789 and much more", TooBig},
		{"12345678", ""},
	} {
		got = nil
		code, out := serveBytes(t, []string{"1", "xlsx"}, []byte(c.input), o)
		res, ref, err := parseReply(out, lim)
		switch {
		case code != exitOK || err != nil:
			t.Errorf("%q: status %d, %v", c.input, code, err)
		case c.refuse == "" && (ref != nil || res.Text != "ok" || string(got) != c.input):
			t.Errorf("%q: refused %v, text %q, reader got %q", c.input, ref, res.Text, got)
		case c.refuse != "" && (ref == nil || ref.Code != c.refuse || got != nil):
			t.Errorf("%q: refusal %v, want %s without the reader", c.input, ref, c.refuse)
		}
	}
}

func TestServeTurnsReaderFailuresIntoRefusals(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTextBytes = 16
	for _, c := range []struct {
		name string
		res  Result
		err  error
		want Code
	}{
		{"panic", Result{}, nil, ReaderFailed},
		{"plain error", Result{}, errors.New("the reader's own words"), ReaderFailed},
		{"unknown code", Result{}, &Refusal{Code: "notACode"}, ReaderFailed},
		{"unknown detail", Result{}, &Refusal{Code: Unsupported, What: "made up"}, ReaderFailed},
		{"known refusal", Result{}, &Refusal{Code: Unsupported, What: WhatDoctype}, Unsupported},
		{"text over the cap", Result{Text: strings.Repeat("x", 17)}, nil, ReaderFailed},
		{"invalid UTF-8", Result{Text: "bad \xff byte"}, nil, ReaderFailed},
		{"fact out of range", Result{Facts: Facts{Rows: MaxFact + 1}}, nil, ReaderFailed},
		{"negative fact", Result{Facts: Facts{Pages: -1}}, nil, ReaderFailed},
		{"cut without a place", Result{Facts: Facts{Cut: true}}, nil, ReaderFailed},
		{"unknown cut place", Result{Facts: Facts{Cut: true, CutAt: "chapters"}}, nil, ReaderFailed},
	} {
		o := inProcess(lim, func(context.Context, Format, []byte, Limits) (Result, error) {
			if c.name == "panic" {
				panic("a reader's bug")
			}
			return c.res, c.err
		})
		code, out := serveBytes(t, []string{"1", "pdf"}, []byte("%PDF-1.7"), o)
		_, ref, err := parseReply(out, lim)
		if code != exitOK || err != nil || ref == nil || ref.Code != c.want {
			t.Errorf("%s: status %d, refusal %v, %v; want %s", c.name, code, ref, err, c.want)
		}
	}
}

// A reader whose engine could not be started gets no reply but a status
// of its own, whatever the context: it is no verdict on the document.
func TestServeEngineStart(t *testing.T) {
	o := inProcess(DefaultLimits(), func(context.Context, Format, []byte, Limits) (Result, error) {
		return Result{}, fmt.Errorf("pdf: %w: wazero: out of memory", errEngineStart)
	})
	if code, out := serveBytes(t, []string{"1", "pdf"}, []byte("%PDF-1.7"), o); code != exitEngine || len(out) != 0 {
		t.Errorf("status %d, output %q", code, out)
	}
	// Extract passes it on as itself, not as readerFailed.
	o.extract = func(ctx context.Context, f Format, data []byte, lim Limits) (Result, error) {
		return extractPDFWith(ctx, data, lim, opener(nil, fmt.Errorf("%w: compile", errEngineStart)))
	}
	if code, out := serveBytes(t, []string{"1", "pdf"}, []byte(fakePDF), o); code != exitEngine || len(out) != 0 {
		t.Errorf("through the wrapper: status %d, output %q", code, out)
	}
}

func TestWatchMemory(t *testing.T) {
	exited := make(chan int, 1)
	go watchMemory(1, time.Millisecond, func(code int) { exited <- code })
	select {
	case code := <-exited:
		if code != exitMemory {
			t.Errorf("exit %d, want %d", code, exitMemory)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watchdog did not fire")
	}
}

func TestParseReply(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTextBytes = 32
	head := func(fields string) string { return `{"v":1,` + fields + "}\n" }
	ok := head(`"refusal":"","what":"","textBytes":5,"facts":{"pages":2}`) + "hello"
	if res, ref, err := parseReply([]byte(ok), lim); err != nil || ref != nil || res.Text != "hello" || res.Facts.Pages != 2 {
		t.Fatalf("good reply: %+v, %v, %v", res, ref, err)
	}
	refusal := head(`"refusal":"tooDeep","what":"","textBytes":0,"facts":{}`)
	if _, ref, err := parseReply([]byte(refusal), lim); err != nil || ref == nil || ref.Code != TooDeep {
		t.Fatalf("refusal: %v, %v", ref, err)
	}
	for name, reply := range map[string]string{
		"empty":            "",
		"no newline":       `{"v":1,"refusal":"","what":"","textBytes":0,"facts":{}}`,
		"not JSON":         "hello\n",
		"header too long":  `{"v":1,"refusal":"","what":"` + strings.Repeat(" ", maxHeaderBytes) + `","textBytes":0,"facts":{}}` + "\n",
		"two values":       `{"v":1,"refusal":"","what":"","textBytes":0,"facts":{}} {}` + "\n",
		"unknown field":    head(`"refusal":"","what":"","textBytes":0,"facts":{},"note":"x"`),
		"unknown fact":     head(`"refusal":"","what":"","textBytes":0,"facts":{"words":1}`),
		"version 0":        `{"v":0,"refusal":"","what":"","textBytes":0,"facts":{}}` + "\n",
		"version 2":        `{"v":2,"refusal":"","what":"","textBytes":0,"facts":{}}` + "\n",
		"no version":       `{"refusal":"","what":"","textBytes":0,"facts":{}}` + "\n",
		"unknown code":     head(`"refusal":"tooSpicy","what":"","textBytes":0,"facts":{}`),
		"unknown detail":   head(`"refusal":"unsupported","what":"flash","textBytes":0,"facts":{}`),
		"detail alone":     head(`"refusal":"","what":"doctype","textBytes":0,"facts":{}`),
		"refusal and text": head(`"refusal":"damaged","what":"","textBytes":2,"facts":{}`) + "hi",
		"refusal and fact": head(`"refusal":"damaged","what":"","textBytes":0,"facts":{"pages":1}`),
		"fact too big":     head(`"refusal":"","what":"","textBytes":0,"facts":{"rows":16777217}`),
		"negative fact":    head(`"refusal":"","what":"","textBytes":0,"facts":{"rows":-1}`),
		"fractional fact":  head(`"refusal":"","what":"","textBytes":0,"facts":{"rows":1.5}`),
		"bad cut place":    head(`"refusal":"","what":"","textBytes":0,"facts":{"cut":true,"cutAt":"pagesX"}`),
		"cut without cut":  head(`"refusal":"","what":"","textBytes":0,"facts":{"cutAt":"pages"}`),
		"text over cap":    head(`"refusal":"","what":"","textBytes":33,"facts":{}`) + strings.Repeat("x", 33),
		"negative text":    head(`"refusal":"","what":"","textBytes":-1,"facts":{}`),
		"short text":       head(`"refusal":"","what":"","textBytes":5,"facts":{}`) + "hell",
		"trailing data":    head(`"refusal":"","what":"","textBytes":5,"facts":{}`) + "hello!",
		"invalid UTF-8":    head(`"refusal":"","what":"","textBytes":5,"facts":{}`) + "hell\xff",
	} {
		if res, ref, err := parseReply([]byte(reply), lim); err == nil {
			t.Errorf("%s: accepted as %+v, %v", name, res, ref)
		}
	}
}

func TestOutcomeString(t *testing.T) {
	seen := map[string]bool{}
	for o := OK; o <= EngineFailed; o++ {
		s := o.String()
		if strings.HasPrefix(s, "Outcome(") || seen[s] {
			t.Errorf("%d: %q", int(o), s)
		}
		seen[s] = true
	}
	if s := (EngineFailed + 1).String(); s != "Outcome(10)" {
		t.Errorf("unknown outcome: %q", s)
	}
}

// --- through a worker process ---------------------------------------------

// runMode runs one extraction in a worker of the test binary behaving as
// mode says ("" for the real worker), and how long it took.
func runMode(t *testing.T, mode string, timeout time.Duration, f Format, data []byte, lim Limits) (Result, *Refusal, Outcome, *os.ProcessState, time.Duration) {
	t.Helper()
	t.Setenv(workerModeEnv, mode)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	res, ref, out, state := run(ctx, testExecutable(t), f, data, lim)
	return res, ref, out, state, time.Since(start)
}

func TestRunReply(t *testing.T) {
	text := "first line\nzweite Zeile \u00fc\n"
	res, ref, out, state, _ := runMode(t, modeEcho, childTimeout, PDF, []byte(text), DefaultLimits())
	if out != OK || ref != nil || res.Text != text || res.Facts != (Facts{Pages: 1, PagesRead: 1}) {
		t.Errorf("got %s, %v, %+v", out, ref, res)
	}
	if state == nil || !state.Success() {
		t.Errorf("worker not reaped cleanly: %v", state)
	}
}

func TestRunRealWorker(t *testing.T) {
	t.Setenv(workerModeEnv, "")
	// The real ServeWorker, in a process of its own, refuses bytes its
	// reader does not take: no PDF header, and a ZIP signature that leads
	// nowhere.
	for f, want := range map[Format]Code{PDF: WrongFormat, DOCX: Damaged, XLSX: Damaged} {
		if _, ref, out := runChild(t, f, []byte("PK\x03\x04 or %PDF-1.7")); out != Refused || ref == nil || ref.Code != want {
			t.Errorf("%s: %s, %v, want %s", f, out, ref, want)
		}
	}
	if _, ref, out := runChild(t, PDF, nil); out != Refused || ref == nil || ref.Code != Damaged {
		t.Errorf("empty input: %s, %v", out, ref)
	}

	// The worker's own cap on stdin: a parent allowing more is refused.
	lim := DefaultLimits()
	lim.MaxInputBytes++
	_, ref, out, state, _ := runMode(t, "", childTimeout, DOCX, make([]byte, lim.MaxInputBytes), lim)
	if out != Refused || ref == nil || ref.Code != TooBig || state == nil {
		t.Errorf("over the input cap: %s, %v, %v", out, ref, state)
	}
}

func TestRunMisbehavingWorker(t *testing.T) {
	const quick = 10 * time.Second // generous for a loaded CI machine
	for _, c := range []struct {
		mode    string
		timeout time.Duration
		data    []byte
		want    Outcome
	}{
		{modeHang, time.Second, nil, Timeout},
		{modeSelfDeadline, childTimeout, nil, Timeout},
		{modePanic, childTimeout, nil, Crashed},
		{modeStackOverflow, childTimeout, nil, Crashed},
		{modeAlloc2GiB, childTimeout, nil, Memory},
		{modeNewerWorker, childTimeout, nil, Mismatch},
		{modeFloodStdout, childTimeout, nil, BadReply},
		{modeBadHeader, childTimeout, nil, BadReply},
		{modeWrongVersion, childTimeout, nil, BadReply},
		{modeShortText, childTimeout, nil, BadReply},
		{modeTrailingData, childTimeout, nil, BadReply},
		{modeExit0NoReply, childTimeout, nil, BadReply},
		{modeEngineStart, childTimeout, nil, EngineFailed},
		{modeReplyEngine, childTimeout, nil, BadReply},
		// More than a pipe holds, so that the parent is still writing
		// when the worker replies and exits.
		{modeIgnoreStdin, childTimeout, make([]byte, 4<<20), OK},
	} {
		t.Run(c.mode, func(t *testing.T) {
			data := c.data
			if data == nil {
				data = []byte("%PDF-1.7 test")
			}
			res, ref, out, state, took := runMode(t, c.mode, c.timeout, PDF, data, DefaultLimits())
			if out != c.want || ref != nil {
				t.Errorf("got %s, %v; want %s", out, ref, c.want)
			}
			if out == OK && res.Text != "ignored" {
				t.Errorf("text %q", res.Text)
			}
			if out != OK && res != (Result{}) {
				t.Errorf("a failure with a result: %+v", res)
			}
			if state == nil {
				t.Error("the worker was not reaped")
			}
			if took > c.timeout+quick {
				t.Errorf("took %v", took)
			}
		})
	}
}

func TestRunCancelKillsWorker(t *testing.T) {
	t.Setenv(workerModeEnv, modeHang)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, _, out, state := run(ctx, testExecutable(t), XLSX, []byte("PK\x03\x04"), DefaultLimits())
	if out != Cancelled {
		t.Errorf("got %s, want %s", out, Cancelled)
	}
	if state == nil || state.Success() {
		t.Errorf("worker state %v", state)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestRunWithoutWorker(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-worker")
	if _, ref, out, state := run(context.Background(), missing, PDF, []byte("%PDF-1.7"), DefaultLimits()); out != SpawnFailed || ref != nil || state != nil {
		t.Errorf("missing executable: %s, %v, %v", out, ref, state)
	}

	exe := testExecutable(t)
	if _, ref, out, state := run(context.Background(), exe, "doc", []byte("x"), DefaultLimits()); out != Refused || ref == nil || ref.Code != Unsupported || state != nil {
		t.Errorf("unknown format: %s, %v, %v", out, ref, state)
	}
	lim := DefaultLimits()
	lim.MaxInputBytes = 4
	if _, ref, out, state := run(context.Background(), exe, PDF, []byte("%PDF-"), lim); out != Refused || ref == nil || ref.Code != TooBig || state != nil {
		t.Errorf("over the input cap: %s, %v, %v", out, ref, state)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, out, state := run(cancelled, exe, PDF, []byte("%PDF-"), DefaultLimits()); out != Cancelled || state != nil {
		t.Errorf("cancelled before: %s, %v", out, state)
	}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, _, out, state := run(expired, exe, PDF, []byte("%PDF-"), DefaultLimits()); out != Timeout || state != nil {
		t.Errorf("expired before: %s, %v", out, state)
	}
}

func TestWorkerRefusesOtherProtocols(t *testing.T) {
	t.Setenv(workerModeEnv, "")
	exe := testExecutable(t)
	for _, args := range [][]string{
		{WorkerArg},
		{WorkerArg, "2", "pdf"},
		{WorkerArg, "1", "odt"},
		{WorkerArg, "1", "pdf", "extra"},
	} {
		cmd := exec.Command(exe, args...)
		cmd.Stdin = strings.NewReader("%PDF-1.7")
		out, err := cmd.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != exitMismatch || len(out) != 0 {
			t.Errorf("%q: %v, output %q", args, err, out)
		}
	}
}
