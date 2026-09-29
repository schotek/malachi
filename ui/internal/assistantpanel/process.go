// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The limits of a Process.
const (
	// stderrLimit is the most of stderr that is kept.
	stderrLimit = 64 << 10
	// reasonLimit cuts the reason of an early exit, stderr's first line.
	reasonLimit = 200
	// DefaultKillGrace is the time from SIGTERM to SIGKILL.
	DefaultKillGrace = 2 * time.Second
	// eofGrace is how long stdout and stderr are read after the exit (a
	// child of Claude Code that inherited them would keep them open).
	eofGrace = 500 * time.Millisecond
	// maxLine is the longest stdout line; a longer one is dropped (a tool
	// result is capped far below this by the bridge).
	maxLine = 16 << 20
)

// Exit is how a Process ended: the exit status, or the negated signal
// number (-15 for SIGTERM); and the first line of its stderr.
type Exit struct {
	Status int
	Reason string
}

// Description is the reason for the transcript: stderr's first line, else
// the status in words (technical, English, like other error details).
func (e Exit) Description() string {
	switch {
	case e.Reason != "":
		return e.Reason
	case e.Status < 0:
		return fmt.Sprintf("claude died of signal %d", -e.Status)
	}
	return fmt.Sprintf("claude exited with status %d", e.Status)
}

// errStarted is Start on a process that was started already.
var errStarted = errors.New("claude was started already")

// Process is one conversation of the panel: a long-lived claude -p with
// stream-json on both sides (assistant.Args). Each Send writes one turn to
// its stdin (assistant.UserMessage and a newline); stdin stays open while
// the conversation lives. Its stdout is read continuously, cut into lines,
// every line parsed with assistant.ParseEvents off the main loop, and the
// events are delivered on the main loop in the order of the lines
// (OnEvents). A line that is not JSON is logged and skipped. Its stderr is
// kept, at most stderrLimit bytes, for the reason of an early exit.
//
// Terminate closes stdin and sends SIGTERM, and SIGKILL after the grace
// when the process is still there. Its end is reported once, after every
// event of its stdout (OnExit), whether it exited by itself, died or was
// terminated. The environment and the working directory are the caller's
// (assistant.ChildEnv, the private directory). A write to a process that
// has gone fails with EPIPE (Go's runtime does not raise SIGPIPE for it)
// and is dropped; the exit reports the rest. (macOS: ClaudeCodeProcess.)
type Process struct {
	// OnEvents gets the events of each stdout line, in order.
	OnEvents func([]assistant.Event)
	// OnExit gets the end of the process, once, after the last events.
	OnExit func(Exit)

	loop       Loop
	log        *slog.Logger
	executable string
	args, env  []string
	dir        string
	killGrace  time.Duration

	cmd   *exec.Cmd
	stdin *stdinWriter
	// running is set from the start until the end was reported;
	// terminating once Terminate was called. Main loop only.
	running, terminating bool
	ended                *Exit
}

// NewProcess prepares claude at executable with args, env and dir; Start
// starts it.
func NewProcess(loop Loop, log *slog.Logger, executable string, args, env []string, dir string, killGrace time.Duration) *Process {
	return &Process{loop: loop, log: log, executable: executable, args: args, env: env, dir: dir, killGrace: killGrace}
}

// Running says whether the process was started and its end not reported
// yet.
func (p *Process) Running() bool { return p.running }

// Ended is how it ended, nil before.
func (p *Process) Ended() *Exit { return p.ended }

// Start starts the process; the error is technical.
func (p *Process) Start() error {
	if p.cmd != nil {
		return errStarted
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("claude could not be started: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		return fmt.Errorf("claude could not be started: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return fmt.Errorf("claude could not be started: %w", err)
	}
	cmd := exec.Command(p.executable, p.args...)
	cmd.Env = p.env
	cmd.Dir = p.dir
	// Files, not writers: the child gets the descriptors themselves, and
	// Wait copies nothing, so it returns when claude exits whatever a
	// grandchild keeps open.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW, errR, errW)
		return fmt.Errorf("claude could not be started: %w", err)
	}
	closeAll(inR, outW, errW)
	p.cmd = cmd
	p.running = true
	p.stdin = newStdinWriter(inW)

	outDone := make(chan struct{})
	errDone := make(chan struct{})
	var stderr limitedBuffer
	go func() {
		defer close(outDone)
		p.readLines(outR)
	}()
	go func() {
		defer close(errDone)
		_, _ = io.Copy(&stderr, errR)
	}()
	go func() {
		_ = cmd.Wait()
		status := exitStatus(cmd.ProcessState)
		stop := time.AfterFunc(eofGrace, func() { closeAll(outR, errR) })
		<-outDone
		<-errDone
		stop.Stop()
		closeAll(outR, errR)
		reason := firstLine(stderr.Bytes(), reasonLimit)
		p.loop.Post(func() { p.finished(Exit{Status: status, Reason: reason}) })
	}()
	return nil
}

// Send writes one turn; false when the process is not running (or being
// terminated). The write happens off the main loop, in order.
func (p *Process) Send(line []byte) bool {
	if !p.running || p.terminating || p.stdin == nil {
		return false
	}
	data := make([]byte, 0, len(line)+1)
	data = append(append(data, line...), '\n')
	p.stdin.write(data)
	return true
}

// Terminate ends the conversation now: stdin closed, SIGTERM, SIGKILL after
// the grace. The end is still reported through OnExit.
func (p *Process) Terminate() {
	if !p.running || p.terminating || p.cmd == nil {
		return
	}
	p.terminating = true
	p.stdin.close()
	proc := p.cmd.Process
	// Once reaped, the process is done for os.Process: neither signal can
	// reach a pid that belongs to somebody else by now.
	_ = proc.Signal(syscall.SIGTERM)
	time.AfterFunc(p.killGrace, func() { _ = proc.Kill() })
}

func (p *Process) finished(e Exit) {
	if !p.running {
		return
	}
	p.running = false
	p.stdin.close()
	p.ended = &e
	if p.OnExit != nil {
		p.OnExit(e)
	}
}

// readLines reads stdout to its end (or until it is closed), cutting lines
// and parsing them, and posts each line's events.
func (p *Process) readLines(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	dropping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !dropping {
			line = append(line, chunk...)
			if len(line) > maxLine {
				p.log.Warn("claude: a stdout line over the limit was dropped", "limit", maxLine)
				line, dropping = nil, true
			}
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err == nil:
			if !dropping {
				p.deliver(line[:len(line)-1])
			}
			line, dropping = nil, false
			continue
		}
		// The end, or the pipe closed: a last line without its newline.
		if len(line) > 0 && !dropping {
			p.deliver(line)
		}
		return
	}
}

// deliver parses one line and posts its events.
func (p *Process) deliver(line []byte) {
	events, err := assistant.ParseEvents(line)
	if err != nil {
		// The line may be mail or model text: only the error is logged.
		p.log.Warn("claude: a stdout line was not read", "err", err)
		return
	}
	if len(events) == 0 {
		return
	}
	p.loop.Post(func() {
		if p.OnEvents != nil {
			p.OnEvents(events)
		}
	})
}

// exitStatus is the status of a process that ended: its exit code, or the
// negated number of the signal that ended it.
func exitStatus(st *os.ProcessState) int {
	if st == nil {
		return -1
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -int(ws.Signal())
	}
	return st.ExitCode()
}

func closeAll(fs ...*os.File) {
	for _, f := range fs {
		_ = f.Close()
	}
}

// limitedBuffer keeps the first stderrLimit bytes written to it and
// discards the rest.
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := stderrLimit - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

// stdinWriter is the write end of the child's stdin: every write and the
// close run on one goroutine, in order, so a close never overtakes a write
// and a write that blocks (the child does not read) never blocks the main
// loop.
type stdinWriter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
}

func newStdinWriter(f *os.File) *stdinWriter {
	w := &stdinWriter{}
	w.cond = sync.NewCond(&w.mu)
	go func() {
		defer f.Close()
		for {
			w.mu.Lock()
			for len(w.queue) == 0 && !w.closed {
				w.cond.Wait()
			}
			if len(w.queue) == 0 {
				w.mu.Unlock()
				return
			}
			data := w.queue[0]
			w.queue = w.queue[1:]
			w.mu.Unlock()
			if _, err := f.Write(data); err != nil {
				// EPIPE: the process is gone; its exit says why.
				w.mu.Lock()
				w.queue, w.closed = nil, true
				w.mu.Unlock()
				return
			}
		}
	}()
	return w
}

func (w *stdinWriter) write(data []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.queue = append(w.queue, data)
	w.cond.Signal()
}

// close closes stdin once the writes before it are done.
func (w *stdinWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.cond.Signal()
}
