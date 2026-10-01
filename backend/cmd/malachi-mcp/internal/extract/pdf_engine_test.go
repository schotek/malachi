// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klippa-app/go-pdfium"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// The PDF engine (pdf_engine.go) with the wrapper, on the fixtures in
// testdata/documents/pdf and on documents made here. What the checks hold
// the engine to is engine-agnostic (text, refusals, facts); the hostile
// documents run in a worker, as the bridge runs them.

var pdfFixtureDir = filepath.Join("..", "..", "..", "..", "testdata", "documents", "pdf")

func readPDFFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pdfFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The text of simple.pdf, which the encrypted fixtures hold too.
const simpleText = "--- page 1 ---\nMalachi Mail test document.\nThe quick brown fox jumps over the lazy dog.\nSecond line: 1234.50 EUR, 30. 9. 2026."

// extractInProcess runs Extract on a PDF in the test's own process.
func extractInProcess(t *testing.T, data []byte, lim Limits) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	return Extract(ctx, PDF, data, lim)
}

func TestPDFEngineFixtures(t *testing.T) {
	canary := "Příliš žluťoučký kůň úpěl ďábelské ódy."
	for _, c := range []struct {
		name     string
		text     string   // the whole text, when given
		contains []string // parts of the text, in this order
		lacks    string
		refusal  *Refusal
		facts    *Facts
	}{
		{name: "simple.pdf", text: simpleText, facts: &Facts{Pages: 1, PagesRead: 1}},
		{name: "multipage.pdf", text: "--- page 1 ---\nPage one says hello to the reader.\nIt has two lines of text.\n\n--- page 2 ---\n\n--- page 3 ---\nPage three ends the document here.\nLast line of the last page.",
			facts: &Facts{Pages: 3, PagesRead: 3, PagesWithoutText: 1}},
		{name: "invisible.pdf", text: "--- page 1 ---\nScanned page with an invisible text layer.\nRecognised words sit under the picture.",
			facts: &Facts{Pages: 1, PagesRead: 1, HiddenContent: true}},
		{name: "scan.pdf", refusal: &Refusal{Code: NoText}},
		{name: "openaction.pdf", text: "--- page 1 ---\nA document with an OpenAction script.\nIts text is read and nothing is run."},
		{name: "brokenxref.pdf", text: "--- page 1 ---\nThe cross-reference table of this file is wrong.\nThe text is still found by a scan."},
		{name: "incremental.pdf", text: "--- page 1 ---\nThis text comes from an incremental update.\nThe reader shows the updated page.", lacks: "original"},
		{name: "cjk.pdf", text: "--- page 1 ---\n中文文本提取测试，这是第一行。内容很短。"},
		// PDFium joins a word broken at a line end; the hyphen stays.
		{name: "hyphen.pdf", text: "--- page 1 ---\nThe extraction of hyphen-ated words at the end of a line is tested here."},
		{name: "garbled.pdf", refusal: &Refusal{Code: Garbled}},
		{name: "mixed.pdf", text: "--- page 1 ---\nThis first page is plain text in Helvetica.\nThe second page cannot be decoded.\n\n--- page 2 ---",
			facts: &Facts{Pages: 2, PagesRead: 2, PagesUndecodable: 1}},
		{name: "pubsec.pdf", refusal: &Refusal{Code: Encrypted}},
		{name: "enc-rc4-40.pdf", text: simpleText},
		{name: "enc-rc4-128.pdf", text: simpleText},
		{name: "enc-aes-128.pdf", text: simpleText},
		{name: "enc-aes-256.pdf", text: simpleText},
		{name: "enc-userpw.pdf", refusal: &Refusal{Code: Encrypted, What: WhatPassword}},
		{name: "invoice-libreoffice.pdf", contains: []string{"--- page 1 ---\nFaktura – daňový doklad č. 2026100458\n",
			"Celkem k úhradě 5 890,07 Kč", canary}},
		{name: "twocol-chrome.pdf", contains: []string{"Věta 00:", "Věta 01:", "Věta 07:", "Věta 08:", "Věta 15:"}},
		{name: "objstm-xrefstream.pdf", contains: []string{"--- page 1 ---\nEnergie Pod Sněžkou, a.s.", "--- page 2 ---\n",
			"PŘÍLIŠ ŽLUŤOUČKÝ KŮŇ ÚPĚL ĎÁBELSKÉ ÓDY.", "Strana 2 z 2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := extractInProcess(t, readPDFFixture(t, c.name), DefaultLimits())
			if c.refusal != nil {
				var r *Refusal
				if !errors.As(err, &r) || *r != *c.refusal {
					t.Fatalf("got %v, want %v", err, c.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.text != "" && res.Text != c.text {
				t.Errorf("text\n%q\nwant\n%q", res.Text, c.text)
			}
			rest := res.Text
			for _, part := range c.contains {
				i := strings.Index(rest, part)
				if i < 0 {
					t.Errorf("%q missing or out of order in\n%s", part, res.Text)
					break
				}
				rest = rest[i+len(part):]
			}
			if c.lacks != "" && strings.Contains(res.Text, c.lacks) {
				t.Errorf("text has %q:\n%s", c.lacks, res.Text)
			}
			if c.facts != nil && res.Facts != *c.facts {
				t.Errorf("facts %+v\nwant  %+v", res.Facts, *c.facts)
			}
			if err := checkWorkerResult(res); err != nil {
				t.Error(err)
			}
		})
	}
}

// checkWorkerResult is what the worker holds a result to before it
// replies.
func checkWorkerResult(res Result) error {
	return checkResult(res, DefaultLimits())
}

func TestPDFEngineDeterministic(t *testing.T) {
	data := readPDFFixture(t, "invoice-libreoffice.pdf")
	first, err := extractInProcess(t, data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	second, err := extractInProcess(t, data, DefaultLimits())
	if err != nil || first != second {
		t.Errorf("a second reading differs: %v\n%q\n%q", err, first.Text, second.Text)
	}
	// The worker reads it the same as this process.
	res, ref, out := runChild(t, PDF, data)
	if out != OK || ref != nil || res != first {
		t.Errorf("in a worker: %s, %v, %+v", out, ref, res.Facts)
	}
}

// testPDF builds a PDF with one page per element of pages, each line of
// it a line of Helvetica text (ASCII). catalog is added to the catalog
// dictionary.
func testPDF(catalog string, pages ...string) []byte {
	var b bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	obj("<< /Type /Catalog /Pages 2 0 R " + catalog + " >>")
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for i, p := range pages {
		var content strings.Builder
		content.WriteString("BT /F1 10 Tf 12 TL 36 756 Td\n")
		for _, line := range strings.Split(p, "\n") {
			line = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(line)
			fmt.Fprintf(&content, "(%s) Tj T*\n", line)
		}
		content.WriteString("ET")
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

// lines is n numbered lines of about width bytes each.
func lines(n, width int) string {
	var b strings.Builder
	for i := range n {
		line := fmt.Sprintf("Line %06d ", i)
		line += strings.Repeat("x", max(width-len(line), 0))
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

func TestPDFEngineLimits(t *testing.T) {
	t.Run("pages", func(t *testing.T) {
		pages := make([]string, 501)
		for i := range pages {
			pages[i] = fmt.Sprintf("This is page number %d of a long document.", i+1)
		}
		res, err := extractInProcess(t, testPDF("", pages...), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if want := (Facts{Pages: 501, PagesRead: 500, Cut: true, CutAt: CutPages, CutPage: 501}); res.Facts != want {
			t.Errorf("facts %+v", res.Facts)
		}
		if !strings.HasSuffix(res.Text, "--- page 500 ---\nThis is page number 500 of a long document.") {
			t.Errorf("text ends %q", res.Text[max(len(res.Text)-100, 0):])
		}
	})
	t.Run("page bytes", func(t *testing.T) {
		// 300 KiB of text on the first page, read in several chunks.
		long := lines(6000, 50)
		res, err := extractInProcess(t, testPDF("", long, "A second, short page of text."), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if res.Facts.PagesCut != 1 || res.Facts.Cut {
			t.Errorf("facts %+v", res.Facts)
		}
		first, second, ok := strings.Cut(res.Text, "\n\n--- page 2 ---\n")
		first = strings.TrimPrefix(first, "--- page 1 ---\n")
		if !ok || second != "A second, short page of text." || len(first) > DefaultLimits().MaxPageBytes || !strings.HasPrefix(long, first+"\n") {
			t.Errorf("page 1 of %d bytes, not a prefix of whole lines, or page 2 %q", len(first), second)
		}
		if len(first) < DefaultLimits().MaxPageBytes-60 {
			t.Errorf("page 1 cut early, at %d bytes", len(first))
		}
	})
	t.Run("text bytes", func(t *testing.T) {
		page := lines(4000, 60) // 240 KB a page, under the page cap
		res, err := extractInProcess(t, testPDF("", page, page, page, page, page), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if f := res.Facts; !f.Cut || f.CutAt != CutTextBytes || f.CutPage != 5 || f.PagesRead != 5 || f.PagesCut != 0 {
			t.Errorf("facts %+v", f)
		}
		if len(res.Text) > DefaultLimits().MaxTextBytes || !strings.HasSuffix(res.Text, "x") {
			t.Errorf("text of %d bytes ends %q", len(res.Text), res.Text[len(res.Text)-20:])
		}
	})
}

func TestPDFEngineDeadline(t *testing.T) {
	bomb := readPDFFixture(t, "flatebomb.pdf")
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Extract(ctx, PDF, bomb, DefaultLimits())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the deadline", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("stopped after %v", took)
	}
	// The process goes on reading documents, and nothing of the stopped
	// one is left running.
	res, err := extractInProcess(t, readPDFFixture(t, "simple.pdf"), DefaultLimits())
	if err != nil || res.Text != simpleText {
		t.Errorf("after the deadline: %v, %q", err, res.Text)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Errorf("%d goroutines, %d before", n, before)
	}
}

// An engine that does not start, for the document or again for the pages
// after it failed inside one, is errEngineStart, not a refusal: the worker
// gives no reply for it (exitEngine), so the bridge neither calls the
// document damaged nor caches it.
func TestPDFEngineStartFailure(t *testing.T) {
	simple := readPDFFixture(t, "simple.pdf")
	start := pdfiumInit
	t.Cleanup(func() { pdfiumInit = start })
	failing := func(webassembly.Config) (pdfium.Pool, error) {
		return nil, errors.New("mmap: cannot allocate memory")
	}

	pdfiumInit = failing
	if _, err := extractInProcess(t, simple, DefaultLimits()); err != errEngineStart {
		t.Errorf("at the start: %v", err)
	}
	if res, ref, noReply := extractSafely(PDF, simple, workerProcess()); noReply != exitEngine || ref != nil || res != (Result{}) {
		t.Errorf("in the worker: %+v, %v, status %d", res, ref, noReply)
	}

	// The engine failed inside a page (a trap), and the fresh one for the
	// pages left does not start.
	pdfiumInit = start
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	doc, err := openPDF(ctx, simple, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	doc.(*pdfiumDoc).broken = true
	pdfiumInit = failing
	if _, _, err := doc.PageText(ctx, 0, DefaultLimits().MaxPageBytes); !errors.Is(err, errEngineStart) {
		t.Errorf("after a restart: %v", err)
	}
}

func TestPDFEngineNoHostFiles(t *testing.T) {
	// PDFium can open a document by path (FPDF_LoadDocument). With the
	// engine's configuration nothing of the host is mounted, so no path
	// opens; with go-pdfium's default (the root mounted) the same call
	// opens the fixture, which shows the check reaches the file system.
	abs, err := filepath.Abs(filepath.Join(pdfFixtureDir, "simple.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	// go-pdfium takes POSIX paths; its default mounts the volume of the
	// working directory, where the fixtures are, as the root.
	path := filepath.ToSlash(strings.TrimPrefix(abs, filepath.VolumeName(abs)))
	open := func(cfg webassembly.Config) error {
		pool, err := webassembly.Init(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = pool.Close() }()
		inst, err := pool.GetInstance(childTimeout)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = inst.Close() }()
		doc, err := inst.OpenDocument(&requests.OpenDocument{FilePath: &path})
		if err == nil {
			_, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
		}
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	if err := open(pdfiumConfig(ctx)); !errors.Is(err, pdfiumerrors.ErrFile) {
		t.Errorf("the engine opened %s: %v", path, err)
	}
	def := pdfiumConfig(ctx)
	def.FSConfig = nil
	if err := open(def); err != nil {
		t.Errorf("go-pdfium's default did not open %s either (%v): the check proves nothing", path, err)
	}
}

func TestPDFEngineHostile(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	noise := make([]byte, 64<<10)
	for i := range noise {
		noise[i] = byte(random.Uint32())
	}
	simple := readPDFFixture(t, "simple.pdf")
	deepContent := "BT /F1 12 Tf 72 720 Td " + strings.Repeat("[", 100_000) + "(x)" + strings.Repeat("]", 100_000) + " TJ ET"
	manyPages := make([]string, 20_000)
	for i := range manyPages {
		manyPages[i] = "p"
	}
	for _, c := range []struct {
		name string
		data []byte
		want Outcome
		code Code   // the refusal, with Refused
		text string // in the text, with OK
	}{
		{"flatebomb.pdf", readPDFFixture(t, "flatebomb.pdf"), Refused, Damaged, ""},
		{"deepnest.pdf", readPDFFixture(t, "deepnest.pdf"), OK, "", "Text on the only real page"},
		{"kidscycle.pdf", readPDFFixture(t, "kidscycle.pdf"), OK, "", "Text on the only real page"},
		{"hugecount.pdf", readPDFFixture(t, "hugecount.pdf"), OK, "", "Text on the only real page"},
		{"prevloop.pdf", readPDFFixture(t, "prevloop.pdf"), OK, "", "Text on the only real page"},
		{"objstmself.pdf", readPDFFixture(t, "objstmself.pdf"), Refused, Damaged, ""},
		{"2M nested arrays", testPDF("/Deep "+strings.Repeat("[", 2_000_000)+strings.Repeat("]", 2_000_000), "Text after deep nesting."), OK, "", "Text after deep nesting."},
		{"100k nested arrays in content", bytes.Replace(testPDF("", "placeholder"), []byte("(placeholder) Tj T*"), []byte(deepContent), 1), Refused, NoText, ""},
		{"20000 pages", testPDF("", manyPages...), Refused, NoText, ""},
		{"noise after the header", append([]byte("%PDF-1.7\n"), noise...), Refused, Damaged, ""},
		{"truncated", simple[:len(simple)/2], Refused, Damaged, ""},
		{"header only", []byte("%PDF-1.4\n%%EOF\n"), Refused, Damaged, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			res, ref, out := runChild(t, PDF, c.data)
			if took := time.Since(start); took > childTimeout {
				t.Errorf("took %v", took)
			}
			if out != c.want {
				t.Fatalf("got %s (%v), want %s", out, ref, c.want)
			}
			switch out {
			case Refused:
				if ref.Code != c.code {
					t.Errorf("refused %v, want %s", ref, c.code)
				}
			case OK:
				if !strings.Contains(res.Text, c.text) {
					t.Errorf("text %q", res.Text)
				}
			}
		})
	}
}

func TestReservedMemory(t *testing.T) {
	const max = pdfMemoryEager * 2
	m := newReservedMemory(1<<20, max).(*reservedMemory)
	b := m.Reallocate(1 << 20)
	if len(b) != 1<<20 || cap(b) != 1<<20 {
		t.Fatalf("first: len %d cap %d", len(b), cap(b))
	}
	b[0], b[len(b)-1] = 1, 2
	// Doubling while small, the contents kept and the new part zero.
	b = m.Reallocate(1<<20 + 1)
	if len(b) != 1<<20+1 || cap(b) != 2<<20 || b[0] != 1 || b[1<<20-1] != 2 || b[1<<20] != 0 {
		t.Fatalf("grown: len %d cap %d", len(b), cap(b))
	}
	// More than double: the size asked for; then up to the eager limit.
	b = m.Reallocate(pdfMemoryEager - 1<<20)
	if cap(b) != pdfMemoryEager-1<<20 || b[0] != 1 {
		t.Fatalf("more than double: cap %d", cap(b))
	}
	b = m.Reallocate(pdfMemoryEager - 1<<19)
	if cap(b) != pdfMemoryEager || b[0] != 1 {
		t.Fatalf("at the eager limit: cap %d", cap(b))
	}
	// Past it, the whole ceiling once; then no copy up to it.
	b = m.Reallocate(pdfMemoryEager + 1)
	if cap(b) != max || b[0] != 1 {
		t.Fatalf("past the eager limit: cap %d", cap(b))
	}
	b[max/2] = 3
	if c := m.Reallocate(max); &c[0] != &b[0] || c[max/2] != 3 || len(c) != max {
		t.Fatal("growing to the ceiling copied")
	}
	if m.Reallocate(max+1) != nil {
		t.Error("over the ceiling")
	}
	m.Free()
	if m.buf != nil {
		t.Error("not freed")
	}
}

// FuzzPDFWorker runs the real engine in a worker of the test binary, as
// the bridge does, on mutations of a few small documents: whatever the
// document, the worker answers within the timeout with text or a refusal
// from the closed sets, or is ended by its memory or time limits; it
// never crashes or breaks the protocol.
func FuzzPDFWorker(f *testing.F) {
	for _, name := range []string{"simple.pdf", "multipage.pdf", "invisible.pdf", "brokenxref.pdf", "garbled.pdf", "enc-rc4-40.pdf"} {
		f.Add(readPDFFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		res, ref, out := runChild(t, PDF, data)
		if took := time.Since(start); took > childTimeout+5*time.Second {
			t.Fatalf("took %v", took)
		}
		switch out {
		case OK:
			if err := checkWorkerResult(res); err != nil {
				t.Fatal(err)
			}
		case Refused:
			if !ref.Valid() {
				t.Fatalf("refusal %v", ref)
			}
		case Timeout, Memory, EngineFailed:
			// The limits, and the machine's own (an engine that does not
			// start is no verdict on the document).
		default:
			t.Fatalf("outcome %s", out)
		}
	})
}
