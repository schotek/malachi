// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The test binary is its own document worker, as the bridge is in
// production: started with extract.WorkerArg first, TestMain serves one
// document and exits, so get_attachment in the tests spawns it through
// extract.Run exactly like the bridge spawns itself.

// The test-only worker modes, read only here (the real ServeWorker knows
// nothing of them): the extract package's tests cover every misbehaviour
// of a worker; these let the bridge's tests choose an outcome.
const (
	workerModeEnv    = "MALACHI_TEST_WORKER_MODE"
	workerFactsEnv   = "MALACHI_TEST_WORKER_FACTS"   // modeEcho: the facts, as JSON
	workerRefusalEnv = "MALACHI_TEST_WORKER_REFUSAL" // modeRefuse: "code" or "code/what"
)

const (
	modeEcho        = "echo"        // the text is the document after its first line
	modeRefuse      = "refuse"      // a refusal from workerRefusalEnv
	modeHang        = "hang"        // reads the document, then never replies
	modePanic       = "panic"       // a goroutine panics: the worker dies
	modeBadHeader   = "badHeader"   // a header line that is not JSON
	modeNewerWorker = "newerWorker" // exits as a worker of another protocol does
	modeNoEngine    = "noEngine"    // exits as a worker whose PDF engine did not start does
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == extract.WorkerArg {
		mode := os.Getenv(workerModeEnv)
		if mode == "" {
			os.Exit(extract.ServeWorker(os.Args[2:], os.Stdin, os.Stdout))
		}
		os.Exit(misbehave(mode))
	}
	// A worker built with -race sleeps a second before a clean exit
	// (ThreadSanitizer's atexit_sleep_ms); the tests' workers need not.
	// The test process itself read GORACE at start, so only they see it.
	if g := os.Getenv("GORACE"); !strings.Contains(g, "atexit_sleep_ms") {
		_ = os.Setenv("GORACE", strings.TrimSpace(g+" atexit_sleep_ms=0"))
	}
	os.Exit(m.Run())
}

// misbehave serves one document the way mode says and returns the exit
// status.
func misbehave(mode string) int {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 2
	}
	switch mode {
	case modeEcho:
		var facts extract.Facts
		if s := os.Getenv(workerFactsEnv); s != "" {
			if err := json.Unmarshal([]byte(s), &facts); err != nil {
				return 2
			}
		}
		_, text, _ := strings.Cut(string(data), "\n")
		return writeTestReply(text, facts, "", "")
	case modeRefuse:
		code, what, _ := strings.Cut(os.Getenv(workerRefusalEnv), "/")
		return writeTestReply("", extract.Facts{}, extract.Code(code), what)
	case modeHang:
		time.Sleep(time.Hour)
	case modePanic:
		go func() { panic("bridge test: a reader's goroutine panics") }()
		time.Sleep(time.Hour)
	case modeBadHeader:
		_, _ = os.Stdout.WriteString("this is not a header\n")
		return 0
	case modeNewerWorker:
		return 5
	case modeNoEngine:
		return 6
	}
	return 1
}

// writeTestReply writes a reply of the worker protocol to stdout.
func writeTestReply(text string, facts extract.Facts, code extract.Code, what string) int {
	h := struct {
		V         int           `json:"v"`
		Refusal   extract.Code  `json:"refusal"`
		What      string        `json:"what"`
		TextBytes int           `json:"textBytes"`
		Facts     extract.Facts `json:"facts"`
	}{extract.ProtocolVersion, code, what, len(text), facts}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(h); err != nil {
		return 2
	}
	if _, err := os.Stdout.WriteString(text); err != nil {
		return 2
	}
	return 0
}

// workerMode makes the workers the test starts behave as mode says.
func workerMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(workerModeEnv, mode)
}

// echoFacts are the facts the echo worker replies with.
func echoFacts(t *testing.T, f extract.Facts) {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(workerFactsEnv, string(b))
}

// shortWorkerTimeout gives the test's workers d instead of workerTimeout.
func shortWorkerTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := workerTimeout
	workerTimeout = d
	t.Cleanup(func() { workerTimeout = old })
}

// pdfDocument is a document the parent's byte check takes for a PDF; the
// echo worker answers text with it.
func pdfDocument(text string) []byte {
	return []byte("%PDF-1.7 test\n" + text)
}

// echoHarness is a harness whose m1 part 4 (report.pdf) is pdfDocument(text)
// and whose workers echo it.
func echoHarness(t *testing.T, text string) *harness {
	t.Helper()
	fb := newFixture()
	setPart(fb, "m1", api.Attachment{PartID: "4", Filename: "report.pdf", ContentType: "application/pdf"}, pdfDocument(text))
	workerMode(t, modeEcho)
	return newHarness(t, fb, false, false)
}

var reportPDF = map[string]any{"accountId": "a1", "messageId": "m1", "partId": "4"}

// The outcomes of a worker that did not answer with a text or a refusal
// are withheld in the bridge's words, quickly, and cached only when they
// would come again for the same bytes.
func TestWorkerFailuresWithheld(t *testing.T) {
	shortWorkerTimeout(t, 500*time.Millisecond)
	for _, c := range []struct {
		mode   string
		want   string
		cached bool
	}{
		{modeHang, "content not returned: reading it took longer than 500ms; the user can open it in Malachi Mail", false},
		{modePanic, "content not returned: the document reader stopped on this file (it may be damaged or built to attack readers)", true},
		{modeBadHeader, "content not returned: the document reader stopped on this file (it may be damaged or built to attack readers)", true},
		{modeNewerWorker, "content not returned: the bridge was updated while it ran; restart the Claude client", false},
		{modeNoEngine, "content not returned: the PDF reader could not be started; call again later; the user can open it in Malachi Mail", false},
	} {
		t.Run(c.mode, func(t *testing.T) {
			h := echoHarness(t, "never shown")
			workerMode(t, c.mode)
			start := time.Now()
			out := h.ok(t, "get_attachment", reportPDF)
			if took := time.Since(start); took > 10*time.Second {
				t.Errorf("took %v", took)
			}
			mustContain(t, out, c.want)
			mustNotContain(t, out, "never shown", "BEGIN UNTRUSTED")
			h.ok(t, "get_attachment", reportPDF)
			want := []string{"get", "part", "get", "part"}
			if c.cached {
				want = want[:3]
			}
			if order, _ := h.calls(); !reflect.DeepEqual(order, want) {
				t.Errorf("calls %v, want %v", order, want)
			}
		})
	}
}

// A refusal of the worker is worded by the bridge, and cached.
func TestWorkerRefusalWithheldAndCached(t *testing.T) {
	h := echoHarness(t, "never shown")
	workerMode(t, modeRefuse)
	t.Setenv(workerRefusalEnv, "encrypted/password")
	out := h.ok(t, "get_attachment", reportPDF)
	mustContain(t, out, "content not returned: the PDF is protected with a password; the user can open it in Malachi Mail")
	t.Setenv(workerRefusalEnv, "noText")
	mustContain(t, h.ok(t, "get_attachment", reportPDF), "the PDF is protected with a password")
	if order, _ := h.calls(); !reflect.DeepEqual(order, []string{"get", "part", "get"}) {
		t.Errorf("calls %v", order)
	}
}

// A worker that cannot be started is withheld and not cached: the next
// call starts one.
func TestWorkerSpawnFailureNotCached(t *testing.T) {
	h := echoHarness(t, "the text")
	old := workerExecutable
	t.Cleanup(func() { workerExecutable = old })
	workerExecutable = func() (string, error) { return "", errors.New("no executable") }
	mustContain(t, h.ok(t, "get_attachment", reportPDF), "content not returned: the document reader could not be started;")
	workerExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "no-such-worker"), nil }
	mustContain(t, h.ok(t, "get_attachment", reportPDF), "content not returned: the document reader could not be started;")
	workerExecutable = old
	mustContain(t, h.ok(t, "get_attachment", reportPDF), "the text")
	if order, _ := h.calls(); !reflect.DeepEqual(order, []string{"get", "part", "get", "part", "get", "part"}) {
		t.Errorf("calls %v", order)
	}
}

// At most maxConcurrentWorkers run at once: a call finding them all busy
// waits for one, up to the pool's wait, then is withheld (not cached).
func TestWorkerSlots(t *testing.T) {
	h := echoHarness(t, "the text")
	if cap(h.b.workers.slots) != maxConcurrentWorkers || h.b.workers.wait != workerWaitTimeout || maxConcurrentWorkers != 2 {
		t.Fatalf("pool of %d slots, wait %v", cap(h.b.workers.slots), h.b.workers.wait)
	}
	for range maxConcurrentWorkers {
		h.b.workers.slots <- struct{}{} // two workers running elsewhere
	}
	h.b.workers.wait = 200 * time.Millisecond
	start := time.Now()
	out := h.ok(t, "get_attachment", reportPDF)
	mustContain(t, out, "content not returned: the document reader is busy with other attachments; call again;")
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("did not wait: %v", took)
	}

	// A slot coming free in time is taken.
	h.b.workers.wait = time.Minute
	time.AfterFunc(300*time.Millisecond, func() { <-h.b.workers.slots })
	start = time.Now()
	mustContain(t, h.ok(t, "get_attachment", reportPDF), "the text")
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Errorf("did not wait for the slot: %v", took)
	}
	if n := len(h.b.workers.slots); n != maxConcurrentWorkers-1 {
		t.Errorf("%d slots taken after the call, want %d", n, maxConcurrentWorkers-1)
	}
	<-h.b.workers.slots
	if order, _ := h.calls(); !reflect.DeepEqual(order, []string{"get", "part", "get", "part"}) {
		t.Errorf("calls %v", order)
	}
}

// A cancelled call kills its worker at once, and a call cancelled while
// waiting for a slot starts none.
func TestWorkerCancelled(t *testing.T) {
	h := echoHarness(t, "")
	workerMode(t, modeHang)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	x := h.b.extractDocument(ctx, extract.PDF, pdfDocument("x"))
	if x.outcome != extract.Cancelled || x.busy || x.cacheable() {
		t.Errorf("got %s busy=%v", x.outcome, x.busy)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %v: the worker was not killed", took)
	}
	if n := len(h.b.workers.slots); n != 0 {
		t.Errorf("%d slots still taken", n)
	}

	for range maxConcurrentWorkers {
		h.b.workers.slots <- struct{}{}
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if x := h.b.extractDocument(ctx, extract.PDF, pdfDocument("x")); x.outcome != extract.Cancelled || x.busy {
		t.Errorf("cancelled while waiting: %s busy=%v", x.outcome, x.busy)
	}
}

// Every refusal and every failure has wording of its own, the bridge's,
// and every outcome is either cached or not on purpose.
func TestWithheldWording(t *testing.T) {
	codes := []extract.Code{
		extract.WrongFormat, extract.Encrypted, extract.OfficeCFB, extract.Damaged, extract.NoText,
		extract.Garbled, extract.TooBig, extract.Expands, extract.TooManyParts, extract.TooDeep,
		extract.TooManyTokens, extract.Unsupported, extract.ReaderFailed,
	}
	whats := []string{
		extract.WhatNone, extract.WhatZipEncryption, extract.WhatCompressionMethod, extract.WhatXMLEncoding,
		extract.WhatDoctype, extract.WhatMacroEnabled, extract.WhatTemplate, extract.WhatPresentation,
		extract.WhatBinaryWorkbook, extract.WhatOtherZip, extract.WhatPassword,
	}
	fallback := refusalReason(extract.PDF, &extract.Refusal{Code: "notACode"})
	seen := map[string]extract.Code{}
	for _, c := range codes {
		if !c.Known() {
			t.Fatalf("%s is no longer a code", c)
		}
		for _, f := range []extract.Format{extract.PDF, extract.DOCX, extract.XLSX} {
			r := refusalReason(f, &extract.Refusal{Code: c})
			if r == "" || r == fallback {
				t.Errorf("%s %s: %q", f, c, r)
			}
			if other, ok := seen[r]; ok && other != c {
				t.Errorf("%s and %s share %q", c, other, r)
			}
			seen[r] = c
		}
	}
	for _, w := range whats {
		if !extract.KnownWhat(w) {
			t.Fatalf("%q is no longer a detail", w)
		}
	}
	for _, c := range []struct {
		code extract.Code
		what string
		want string
	}{
		{extract.WrongFormat, extract.WhatMacroEnabled, "macro-enabled"},
		{extract.WrongFormat, extract.WhatTemplate, "template"},
		{extract.WrongFormat, extract.WhatPresentation, "PowerPoint"},
		{extract.WrongFormat, extract.WhatBinaryWorkbook, ".xlsb"},
		{extract.WrongFormat, extract.WhatOtherZip, "the content is not an Excel workbook (.xlsx)"},
		{extract.Unsupported, extract.WhatZipEncryption, "encrypted"},
		{extract.Unsupported, extract.WhatCompressionMethod, "compression method"},
		{extract.Unsupported, extract.WhatXMLEncoding, "not UTF-8"},
		{extract.Unsupported, extract.WhatDoctype, "DOCTYPE"},
		{extract.Encrypted, extract.WhatPassword, "protected with a password"},
		{extract.OfficeCFB, extract.WhatNone, "older binary format (.doc, .xls)"},
	} {
		if r := refusalReason(extract.XLSX, &extract.Refusal{Code: c.code, What: c.what}); !strings.Contains(r, c.want) {
			t.Errorf("%s/%s: %q, want %q", c.code, c.what, r, c.want)
		}
	}

	failures := map[string]bool{}
	for o := extract.OK; o <= extract.EngineFailed; o++ {
		x := extraction{outcome: o}
		r := workerFailureReason(x)
		switch o {
		case extract.OK, extract.Refused, extract.Cancelled:
			if r != "" {
				t.Errorf("%s: %q", o, r)
			}
		default:
			if r == "" {
				t.Errorf("%s: no wording", o)
			}
			failures[r] = true
		}
		want := o == extract.OK || o == extract.Refused || o == extract.Crashed || o == extract.BadReply || o == extract.Memory
		if x.cacheable() != want {
			t.Errorf("%s: cacheable %v", o, x.cacheable())
		}
	}
	if len(failures) != 6 { // timeout, memory, crashed and badReply alike, spawnFailed, mismatch, engineFailed
		t.Errorf("%d distinct failure texts: %v", len(failures), failures)
	}
	if busy := (extraction{busy: true}); busy.cacheable() || !strings.Contains(workerFailureReason(busy), "busy") {
		t.Errorf("busy: %v, %q", busy.cacheable(), workerFailureReason(busy))
	}
}
