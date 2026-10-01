// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test binary is its own worker, as the bridge is in production: run
// with WorkerArg first, TestMain serves one document and exits, so the
// tests (and the readers' tests of hostile documents) can spawn it through
// Run like the bridge spawns itself.

// workerModeEnv makes a worker started by a test misbehave on purpose. It
// is read only here, in the test binary: the real ServeWorker knows
// nothing of it.
const workerModeEnv = "MALACHI_TEST_WORKER_MODE"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WorkerArg {
		mode := os.Getenv(workerModeEnv)
		if mode == "" {
			os.Exit(ServeWorker(os.Args[2:], os.Stdin, os.Stdout))
		}
		os.Exit(misbehave(mode, os.Args[2:]))
	}
	// A worker built with -race sleeps a second before a clean exit
	// (ThreadSanitizer's atexit_sleep_ms); the tests' workers need not.
	// The test process itself read GORACE at start, so only they see it.
	if g := os.Getenv("GORACE"); !strings.Contains(g, "atexit_sleep_ms") {
		_ = os.Setenv("GORACE", strings.TrimSpace(g+" atexit_sleep_ms=0"))
	}
	os.Exit(m.Run())
}

// childTimeout bounds one worker started by runChild. A variable, so that
// a test of a slow reader can give it longer.
var childTimeout = 20 * time.Second

// runChild extracts data in a worker started from the test binary, with
// DefaultLimits() and childTimeout, as the bridge would: for the readers'
// tests of documents that may crash, hang or exhaust a reader.
func runChild(t testing.TB, f Format, data []byte) (Result, *Refusal, Outcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	res, ref, out, _ := run(ctx, testExecutable(t), f, data, DefaultLimits())
	return res, ref, out
}

// testExecutable is the path of the test binary, the worker's executable.
func testExecutable(t testing.TB) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

// The misbehaviours of workerModeEnv.
const (
	modeEcho          = "echo"          // a reader that returns the document as its text
	modeHang          = "hang"          // a reader that never returns
	modeSelfDeadline  = "selfDeadline"  // a reader that never returns, under a short own deadline
	modePanic         = "panic"         // a panic in a goroutine of the reader: not recoverable
	modeStackOverflow = "stackOverflow" // unbounded recursion: a Go fatal error
	modeAlloc2GiB     = "alloc2GiB"     // a reader allocating up to 2 GiB, for the watchdog to stop
	modeNewerWorker   = "newerWorker"   // a worker speaking the next protocol version
	modeFloodStdout   = "floodStdout"   // endless output instead of a reply
	modeBadHeader     = "badHeader"     // a header line that is not JSON
	modeWrongVersion  = "wrongVersion"  // a well-formed reply of another protocol version
	modeShortText     = "shortText"     // fewer text bytes than the header says
	modeTrailingData  = "trailingData"  // more bytes than the header says
	modeIgnoreStdin   = "ignoreStdin"   // a valid reply without reading the document
	modeExit0NoReply  = "exit0NoReply"  // exit 0 without a reply
	modeEngineStart   = "engineStart"   // a reader whose engine cannot be started
	modeReplyEngine   = "replyEngine"   // a reply, then the status of an engine that did not start
)

// misbehave serves one document the way mode says and returns the exit
// status. The modes inside the reader run under the real worker's guards.
func misbehave(mode string, args []string) int {
	o := workerProcess()
	hang := func(context.Context, Format, []byte, Limits) (Result, error) {
		time.Sleep(time.Hour)
		return Result{}, nil
	}
	switch mode {
	case modeEcho:
		o.extract = func(_ context.Context, _ Format, data []byte, _ Limits) (Result, error) {
			return Result{Text: string(data), Facts: Facts{Pages: 1, PagesRead: 1}}, nil
		}
	case modeHang:
		o.extract = hang
	case modeSelfDeadline:
		o.deadline = 200 * time.Millisecond
		o.extract = hang
	case modePanic:
		o.extract = func(ctx context.Context, f Format, data []byte, lim Limits) (Result, error) {
			go func() { panic("extract test: a reader's goroutine panics") }()
			return hang(ctx, f, data, lim)
		}
	case modeStackOverflow:
		o.extract = func(context.Context, Format, []byte, Limits) (Result, error) {
			return Result{}, fmt.Errorf("never: %d", recurse(0))
		}
	case modeAlloc2GiB:
		o.extract = func(ctx context.Context, f Format, data []byte, lim Limits) (Result, error) {
			var hoard [][]byte
			for total := int64(0); total < 2<<30; total += 64 << 20 {
				hoard = append(hoard, make([]byte, 64<<20))
				time.Sleep(5 * time.Millisecond)
			}
			runtime.KeepAlive(hoard)
			return hang(ctx, f, data, lim)
		}
	case modeNewerWorker:
		o.version = ProtocolVersion + 1
	case modeFloodStdout:
		_, _ = io.Copy(io.Discard, os.Stdin)
		chunk := bytes.Repeat([]byte("flood "), 10<<10)
		for {
			// Errors are ignored on purpose: the parent has to kill it.
			_, _ = os.Stdout.Write(chunk)
		}
	case modeBadHeader:
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = os.Stdout.WriteString("this is not a header\n")
		return exitOK
	case modeWrongVersion:
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = fmt.Fprintf(os.Stdout, `{"v":%d,"refusal":"","what":"","textBytes":2,"facts":{}}`+"\nhi", ProtocolVersion+1)
		return exitOK
	case modeShortText:
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = os.Stdout.WriteString(`{"v":1,"refusal":"","what":"","textBytes":10,"facts":{}}` + "\nshort")
		return exitOK
	case modeTrailingData:
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = os.Stdout.WriteString(`{"v":1,"refusal":"","what":"","textBytes":4,"facts":{}}` + "\nfour and more")
		return exitOK
	case modeIgnoreStdin:
		if err := writeReply(os.Stdout, Result{Text: "ignored"}, nil); err != nil {
			return exitCrash
		}
		return exitOK
	case modeExit0NoReply:
		_, _ = io.Copy(io.Discard, os.Stdin)
		return exitOK
	case modeEngineStart:
		o.extract = func(context.Context, Format, []byte, Limits) (Result, error) {
			return Result{}, fmt.Errorf("%w: no executable memory", errEngineStart)
		}
	case modeReplyEngine:
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := writeReply(os.Stdout, Result{Text: "hi"}, nil); err != nil {
			return exitCrash
		}
		return exitEngine
	default:
		return 1
	}
	return serve(args, os.Stdin, os.Stdout, o)
}

// recurse never returns: each call needs a frame of its own, until the
// stack limit makes the runtime end the process.
func recurse(n int) int {
	if n == -1 {
		return 0
	}
	var pad [256]byte
	pad[n%len(pad)] = byte(n)
	return recurse(n+1) + int(pad[(n*7)%len(pad)])
}
