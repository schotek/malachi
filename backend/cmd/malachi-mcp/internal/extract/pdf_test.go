// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// The PDF wrapper (pdf.go) against a fake engine: every rule that does not
// depend on PDFium. pdf_engine_test.go runs the real engine.

// fakePage is a page of a fakeDoc.
type fakePage struct {
	text  string
	err   error // what PageText returns instead of text
	panic bool  // PageText panics
}

// fakeDoc is a pdfDoc whose pages are given. PageText holds to max as the
// engine does: at most max bytes, cut when the page had more.
type fakeDoc struct {
	pages    []fakePage
	count    int // NumPages when not len(pages)
	hidden   bool
	asked    []int // the pages PageText was asked for
	maxes    []int
	closed   int
	cancel   context.CancelFunc // called when page cancelAt is asked for
	cancelAt int
}

func (d *fakeDoc) NumPages() int {
	if d.count != 0 {
		return d.count
	}
	return len(d.pages)
}

func (d *fakeDoc) PageText(ctx context.Context, i, max int) (string, bool, error) {
	d.asked = append(d.asked, i)
	d.maxes = append(d.maxes, max)
	if d.cancel != nil && i == d.cancelAt {
		d.cancel()
		return "", false, ctx.Err()
	}
	if i >= len(d.pages) {
		return "", false, errPDFPage
	}
	p := d.pages[i]
	if p.panic {
		panic("pdf test: the engine panics")
	}
	if p.err != nil {
		return "", false, p.err
	}
	if len(p.text) > max {
		return runePrefix(p.text, max), true, nil
	}
	return p.text, false, nil
}

func (d *fakeDoc) HiddenText() bool { return d.hidden }
func (d *fakeDoc) Close()           { d.closed++ }

// opener is a pdfOpener giving doc, or err.
func opener(doc *fakeDoc, err error) pdfOpener {
	return func(context.Context, []byte, Limits) (pdfDoc, error) {
		if err != nil {
			return nil, err
		}
		return doc, nil
	}
}

const fakePDF = "%PDF-1.7 fake"

// readFake runs the wrapper on doc with lim.
func readFake(t *testing.T, doc *fakeDoc, lim Limits) (Result, error) {
	t.Helper()
	res, err := extractPDFWith(context.Background(), []byte(fakePDF), lim, opener(doc, nil))
	if doc.closed != 1 {
		t.Errorf("document closed %d times", doc.closed)
	}
	return res, err
}

// refusalCode is the code of a *Refusal error, or a description of what
// else err is.
func refusalCode(err error) Code {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return Code(fmt.Sprintf("not a refusal: %v", err))
}

// sentence is a line of ordinary text, long enough to count as text.
func sentence(n int) string { return fmt.Sprintf("Line %d of a page with ordinary text on it.", n) }

func TestPDFMarkersAndOrder(t *testing.T) {
	doc := &fakeDoc{pages: []fakePage{
		{text: sentence(1) + "\n" + sentence(2)},
		{text: "\r\n" + sentence(3) + "   \r\n\r\n\r\n" + sentence(4) + "\r\n"},
		{text: sentence(5)},
	}}
	res, err := readFake(t, doc, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := "--- page 1 ---\n" + sentence(1) + "\n" + sentence(2) +
		"\n\n--- page 2 ---\n" + sentence(3) + "\n\n" + sentence(4) +
		"\n\n--- page 3 ---\n" + sentence(5)
	if res.Text != want {
		t.Errorf("text\n%q\nwant\n%q", res.Text, want)
	}
	if want := (Facts{Pages: 3, PagesRead: 3}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}
	if !reflect.DeepEqual(doc.asked, []int{0, 1, 2}) {
		t.Errorf("pages asked %v", doc.asked)
	}
	for _, m := range doc.maxes {
		if m != DefaultLimits().MaxPageBytes {
			t.Errorf("max %d asked for a page", m)
		}
	}
}

func TestPDFHeader(t *testing.T) {
	for _, data := range []string{"", "%PDF", "%pdf-1.7", " %PDF-1.7", "\xef\xbb\xbf%PDF-1.7", "PK\x03\x04", "%!PS-Adobe-3.0"} {
		opened := false
		open := func(context.Context, []byte, Limits) (pdfDoc, error) {
			opened = true
			return &fakeDoc{}, nil
		}
		if _, err := extractPDFWith(context.Background(), []byte(data), DefaultLimits(), open); refusalCode(err) != WrongFormat || opened {
			t.Errorf("%q: %v, engine called %v", data, err, opened)
		}
	}
}

func TestPDFOpenErrors(t *testing.T) {
	engineFailed := fmt.Errorf("%w: a trap", errPDFEngine)
	for _, c := range []struct {
		err  error
		want *Refusal
	}{
		{errPDFEncrypted, &Refusal{Code: Encrypted, What: WhatPassword}},
		{fmt.Errorf("open: %w", errPDFEncrypted), &Refusal{Code: Encrypted, What: WhatPassword}},
		{errPDFSecurity, &Refusal{Code: Encrypted}},
		{errPDFDamaged, &Refusal{Code: Damaged}},
	} {
		_, err := extractPDFWith(context.Background(), []byte(fakePDF), DefaultLimits(), opener(nil, c.err))
		var r *Refusal
		if !errors.As(err, &r) || *r != *c.want {
			t.Errorf("%v: got %v, want %v", c.err, err, c.want)
		}
	}
	// The engine failing is not a refusal: Extract makes it readerFailed.
	_, err := extractPDFWith(context.Background(), []byte(fakePDF), DefaultLimits(), opener(nil, engineFailed))
	if !errors.Is(err, errPDFEngine) {
		t.Errorf("engine failure: %v", err)
	}
	if got := refusalCode(err); got.Known() {
		t.Errorf("engine failure became refusal %s", got)
	}

	// An engine that does not start is neither: no verdict on the document.
	_, err = extractPDFWith(context.Background(), []byte(fakePDF), DefaultLimits(), opener(nil, fmt.Errorf("%w: compile", errEngineStart)))
	if !errors.Is(err, errEngineStart) || errors.Is(err, errPDFEngine) || refusalCode(err).Known() {
		t.Errorf("engine start: %v", err)
	}
}

// A fresh engine that does not start for the pages left (after the engine
// failed inside a page) ends the reading without text: the pages read are
// not passed off as the document, nor the rest counted undecodable.
func TestPDFEngineStartAfterRestart(t *testing.T) {
	doc := &fakeDoc{pages: []fakePage{
		{text: sentence(1)},
		{err: fmt.Errorf("%w: opening the document again: no memory", errEngineStart)},
		{text: sentence(3)},
	}}
	res, err := readFake(t, doc, DefaultLimits())
	if !errors.Is(err, errEngineStart) || res != (Result{}) {
		t.Errorf("got %+v, %v", res, err)
	}
	if !reflect.DeepEqual(doc.asked, []int{0, 1}) {
		t.Errorf("pages asked %v", doc.asked)
	}
}

func TestPDFEmptyAndUnreadable(t *testing.T) {
	for name, doc := range map[string]*fakeDoc{
		"no pages":        {},
		"negative count":  {count: -3},
		"all unreadable":  {pages: []fakePage{{err: errPDFPage}, {err: errPDFPage}}},
		"pages not there": {count: 4},
	} {
		if _, err := readFake(t, doc, DefaultLimits()); refusalCode(err) != Damaged {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPDFNoText(t *testing.T) {
	// A scan: no page has as many as minTextChars non-space characters,
	// whatever the pages have in all.
	short := strings.Repeat("x", minTextChars-1)
	doc := &fakeDoc{pages: []fakePage{{text: ""}, {text: "  3  \n"}, {text: short}, {text: "Seite 4 / 4"}}}
	if _, err := readFake(t, doc, DefaultLimits()); refusalCode(err) != NoText {
		t.Errorf("scan: %v", err)
	}
	// An unreadable page among them does not make it damaged.
	doc = &fakeDoc{pages: []fakePage{{text: "1"}, {err: errPDFPage}}}
	if _, err := readFake(t, doc, DefaultLimits()); refusalCode(err) != NoText {
		t.Errorf("scan with an unreadable page: %v", err)
	}
	// One page of text is enough; the others count as without text and
	// keep what little they have.
	doc = &fakeDoc{pages: []fakePage{{text: "Cover"}, {text: ""}, {text: strings.Repeat("x", minTextChars)}}}
	res, err := readFake(t, doc, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- page 1 ---\nCover\n\n--- page 2 ---\n\n--- page 3 ---\n" + strings.Repeat("x", minTextChars); res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if want := (Facts{Pages: 3, PagesRead: 3, PagesWithoutText: 2}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}
}

func TestPDFGarbledPages(t *testing.T) {
	// 20 non-space characters: 2 bad ones are 10 %, allowed; 3 are not.
	ok := strings.Repeat("a", 18) + "\ufffd\ue000"
	bad := strings.Repeat("a", 17) + "\ufffd\ue000\U000F0000"
	controls := strings.Repeat("a", 17) + "\x01\x02\x7f"
	doc := &fakeDoc{pages: []fakePage{{text: ok}, {text: bad}, {text: controls}, {text: sentence(4)}}}
	res, err := readFake(t, doc, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := "--- page 1 ---\n" + ok + "\n\n--- page 2 ---\n\n--- page 3 ---\n\n--- page 4 ---\n" + sentence(4)
	if res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if want := (Facts{Pages: 4, PagesRead: 4, PagesUndecodable: 2}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}

	// A page too short to judge is kept as it is, however bad.
	short := strings.Repeat("\ue000", minTextChars-1)
	doc = &fakeDoc{pages: []fakePage{{text: short}, {text: sentence(2)}}}
	res, err = readFake(t, doc, DefaultLimits())
	if err != nil || !strings.Contains(res.Text, short) || res.Facts.PagesUndecodable != 0 || res.Facts.PagesWithoutText != 1 {
		t.Errorf("short page: %v, %+v", err, res)
	}

	// The limit is the one in Limits.
	lim := DefaultLimits()
	lim.MaxGarbledPercent = 20
	doc = &fakeDoc{pages: []fakePage{{text: bad}}}
	if res, err := readFake(t, doc, lim); err != nil || res.Facts.PagesUndecodable != 0 {
		t.Errorf("20 %%: %v, %+v", err, res.Facts)
	}

	// No usable page: garbled, also beside empty and unreadable pages.
	for name, pages := range map[string][]fakePage{
		"all garbled":            {{text: bad}, {text: controls}},
		"garbled and empty":      {{text: ""}, {text: bad}},
		"garbled and unreadable": {{err: errPDFPage}, {text: bad}},
	} {
		if _, err := readFake(t, &fakeDoc{pages: pages}, DefaultLimits()); refusalCode(err) != Garbled {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPDFUnreadablePages(t *testing.T) {
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}, {err: errPDFPage}, {text: sentence(3)}}}
	res, err := readFake(t, doc, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- page 1 ---\n" + sentence(1) + "\n\n--- page 2 ---\n\n--- page 3 ---\n" + sentence(3); res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if want := (Facts{Pages: 3, PagesRead: 3, PagesUndecodable: 1}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}
}

func TestPDFTextNormalised(t *testing.T) {
	// A lone CR is a line end, not a bad character; invalid UTF-8 shows
	// as U+FFFD; the rest is the textBuilder's.
	text := "first line\rsecond line\r\nthird line \xff\n"
	res, err := readFake(t, &fakeDoc{pages: []fakePage{{text: text}}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- page 1 ---\nfirst line\nsecond line\nthird line \ufffd"; res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if !utf8.ValidString(res.Text) || res.Facts.PagesUndecodable != 0 {
		t.Errorf("facts %+v", res.Facts)
	}
}

func TestPDFHiddenText(t *testing.T) {
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}}, hidden: true}
	res, err := readFake(t, doc, DefaultLimits())
	if err != nil || !res.Facts.HiddenContent {
		t.Errorf("%v, %+v", err, res.Facts)
	}
}

func TestPDFPageCap(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxPages = 2
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}, {text: sentence(2)}, {text: sentence(3)}, {text: sentence(4)}}}
	res, err := readFake(t, doc, lim)
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- page 1 ---\n" + sentence(1) + "\n\n--- page 2 ---\n" + sentence(2); res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if want := (Facts{Pages: 4, PagesRead: 2, Cut: true, CutAt: CutPages, CutPage: 3}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}
	if !reflect.DeepEqual(doc.asked, []int{0, 1}) {
		t.Errorf("pages asked %v", doc.asked)
	}

	// A count the engine reports over MaxFact is saturated.
	doc = &fakeDoc{pages: []fakePage{{text: sentence(1)}}, count: 1 << 30}
	res, err = readFake(t, doc, lim)
	if err != nil || res.Facts.Pages != MaxFact || res.Facts.Validate() != nil {
		t.Errorf("huge count: %v, %+v", err, res.Facts)
	}
}

func TestPDFPageBytesCap(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxPageBytes = 100
	long := sentence(1) + "\n" + sentence(2) + "\n" + sentence(3) // 44 bytes a line
	oneLine := strings.Repeat("word ", 30)
	doc := &fakeDoc{pages: []fakePage{{text: long}, {text: oneLine}, {text: sentence(3)}}}
	res, err := readFake(t, doc, lim)
	if err != nil {
		t.Fatal(err)
	}
	// The long page is cut at its last whole line; a line longer than the
	// cap inside, at a character.
	want := "--- page 1 ---\n" + sentence(1) + "\n" + sentence(2) +
		"\n\n--- page 2 ---\n" + strings.TrimSpace(oneLine[:100]) +
		"\n\n--- page 3 ---\n" + sentence(3)
	if res.Text != want {
		t.Errorf("text\n%q\nwant\n%q", res.Text, want)
	}
	if want := (Facts{Pages: 3, PagesRead: 3, PagesCut: 2}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}

	// The wrapper holds to the cap even when the engine does not, and cuts
	// at a character boundary.
	if got, cut := pageText(strings.Repeat("ž", 60), false, 101); got != strings.Repeat("ž", 50) || !cut {
		t.Errorf("an engine over the cap: %q, %v", got, cut)
	}
}

func TestPDFTextCap(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTextBytes = 150
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}, {text: sentence(2) + "\n" + sentence(3)}, {text: sentence(4)}}}
	res, err := readFake(t, doc, lim)
	if err != nil {
		t.Fatal(err)
	}
	// It stops at the last whole line that fits, inside page 2.
	if want := "--- page 1 ---\n" + sentence(1) + "\n\n--- page 2 ---\n" + sentence(2); res.Text != want {
		t.Errorf("text %q", res.Text)
	}
	if want := (Facts{Pages: 3, PagesRead: 2, Cut: true, CutAt: CutTextBytes, CutPage: 2}); res.Facts != want {
		t.Errorf("facts %+v", res.Facts)
	}
	if !reflect.DeepEqual(doc.asked, []int{0, 1}) {
		t.Errorf("pages asked after the cut: %v", doc.asked)
	}

	// The text cap comes first, so the page cap is not reported.
	lim.MaxPages = 2
	doc.asked, doc.closed = nil, 0
	if res, err := readFake(t, doc, lim); err != nil || res.Facts.CutAt != CutTextBytes {
		t.Errorf("both caps: %v, %+v", err, res.Facts)
	}

	// A marker that no longer fits cuts before the page.
	lim = DefaultLimits()
	lim.MaxTextBytes = len("--- page 1 ---\n"+sentence(1)) + 5
	doc = &fakeDoc{pages: []fakePage{{text: sentence(1)}, {text: sentence(2)}}}
	res, err = readFake(t, doc, lim)
	if err != nil || res.Text != "--- page 1 ---\n"+sentence(1) || res.Facts.CutPage != 2 || res.Facts.CutAt != CutTextBytes {
		t.Errorf("marker cut: %v, %q, %+v", err, res.Text, res.Facts)
	}
}

func TestPDFContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}, {text: sentence(2)}, {text: sentence(3)}}, cancel: cancel, cancelAt: 1}
	_, err := extractPDFWith(ctx, []byte(fakePDF), DefaultLimits(), opener(doc, nil))
	if !errors.Is(err, context.Canceled) || doc.closed != 1 {
		t.Errorf("got %v, closed %d", err, doc.closed)
	}
	if !reflect.DeepEqual(doc.asked, []int{0, 1}) {
		t.Errorf("pages asked %v", doc.asked)
	}

	// The engine's own deadline, the caller's context still alive: an
	// error, not text, which Extract makes readerFailed.
	doc = &fakeDoc{pages: []fakePage{{text: sentence(1)}, {err: context.DeadlineExceeded}}}
	if _, err := readFake(t, doc, DefaultLimits()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("engine deadline: %v", err)
	}
}

func TestPDFDeterministic(t *testing.T) {
	pages := []fakePage{{text: sentence(1)}, {text: strings.Repeat("\ue000", 40)}, {err: errPDFPage}, {text: ""}, {text: sentence(5)}}
	lim := DefaultLimits()
	lim.MaxTextBytes = 120
	first, err1 := readFake(t, &fakeDoc{pages: pages}, lim)
	second, err2 := readFake(t, &fakeDoc{pages: pages}, lim)
	if first != second || fmt.Sprint(err1) != fmt.Sprint(err2) {
		t.Errorf("%+v, %v\n%+v, %v", first, err1, second, err2)
	}
}

func TestPDFEnginePanicIsReaderFailed(t *testing.T) {
	// The wrapper does not recover (fuzzing should see a panic); the
	// worker turns it into readerFailed.
	doc := &fakeDoc{pages: []fakePage{{text: sentence(1)}, {panic: true}}}
	o := workerOptions{version: ProtocolVersion, limits: DefaultLimits(), extract: func(ctx context.Context, _ Format, data []byte, lim Limits) (Result, error) {
		return extractPDFWith(ctx, data, lim, opener(doc, nil))
	}}
	res, ref, noReply := extractSafely(PDF, []byte(fakePDF), o)
	if ref == nil || ref.Code != ReaderFailed || res != (Result{}) || noReply != exitOK {
		t.Errorf("got %+v, %v, %d", res, ref, noReply)
	}
	if doc.closed != 1 {
		t.Errorf("document closed %d times", doc.closed)
	}
}

// FuzzPDFWrapper drives the wrapper with a fake engine built from the
// input: the limits from its first bytes, then pages separated by NUL,
// each one's first byte choosing text, an unreadable page or an engine
// failure. Whatever it is, the wrapper gives a refusal from the closed set
// or text within the caps, the same twice.
func FuzzPDFWrapper(f *testing.F) {
	f.Add([]byte("\x05\x40\x40Ttext on page one, long enough to count\x00T\x00Eunreadable\x00Tfour"))
	f.Add([]byte("\x01\x01\x01T" + strings.Repeat("\ue000", 30)))
	f.Add([]byte("\x02\xff\x08Tline\r\nline\rline\n\n\n\x00T\xff\xfe\x00Fx"))
	f.Add([]byte("\x03\x10\x02Tword word word word word word word word\x00Tmore words to read here please"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 3 {
			return
		}
		lim := DefaultLimits()
		lim.MaxPages = int(data[0]%12) + 1
		lim.MaxTextBytes = int(data[1])*4 + 1
		lim.MaxPageBytes = int(data[2])*2 + 1
		var pages []fakePage
		for _, p := range strings.Split(string(data[3:]), "\x00") {
			switch {
			case p == "":
				pages = append(pages, fakePage{})
			case p[0] == 'E':
				pages = append(pages, fakePage{err: errPDFPage})
			case p[0] == 'F':
				pages = append(pages, fakePage{err: errPDFEngine})
			default:
				pages = append(pages, fakePage{text: p[1:]})
			}
		}
		run := func() (Result, error) {
			doc := &fakeDoc{pages: pages, hidden: len(data)%2 == 0}
			res, err := extractPDFWith(context.Background(), []byte(fakePDF), lim, opener(doc, nil))
			if doc.closed != 1 {
				t.Fatalf("document closed %d times", doc.closed)
			}
			for _, i := range doc.asked {
				if i < 0 || i >= lim.MaxPages {
					t.Fatalf("page %d asked for, MaxPages %d", i, lim.MaxPages)
				}
			}
			return res, err
		}
		res, err := run()
		again, errAgain := run()
		if res != again || fmt.Sprint(err) != fmt.Sprint(errAgain) {
			t.Fatalf("not deterministic: %+v, %v / %+v, %v", res, err, again, errAgain)
		}
		if err != nil {
			var r *Refusal
			if errors.As(err, &r) {
				if !r.Valid() {
					t.Fatalf("refusal outside the closed sets: %v", r)
				}
			} else if !errors.Is(err, errPDFEngine) {
				t.Fatalf("an error that is neither a refusal nor the engine's: %v", err)
			}
			return
		}
		if checkResult(res, lim) != nil {
			t.Fatalf("result breaks the protocol: %v", checkResult(res, lim))
		}
		if res.Facts.PagesRead > lim.MaxPages || res.Facts.PagesRead > res.Facts.Pages {
			t.Fatalf("facts %+v", res.Facts)
		}
		if n := strings.Count(res.Text, "--- page "); n < res.Facts.PagesRead-1 {
			t.Fatalf("%d markers for %d pages read", n, res.Facts.PagesRead)
		}
	})
}
