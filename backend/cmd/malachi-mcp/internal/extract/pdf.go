// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The PDF reader: whatever the engine (pdf_engine.go), the text of the
// pages in the engine's reading order, each page after a marker line
//
//	--- page N ---
//
// with a blank line before every marker but the first. A document can
// imitate a marker inside its own text; the bridge's trusted line outside
// the fence has the true counts.
//
// What the reader decides itself, not the engine:
//   - only bytes starting with "%PDF-" are read (wrongFormat otherwise);
//   - at most MaxPages pages are read, at most MaxPageBytes of the text of
//     each (a longer page is cut at its last line that fits, and counted);
//   - a page whose text is mostly undecodable (U+FFFD, private use,
//     controls: text.go's countChars) is left out and counted, its marker
//     kept, since that is what a font without a character map gives;
//   - a page with fewer than minTextChars non-space characters counts as
//     a page without text (a scan, a picture), its few characters kept;
//   - a page the engine cannot read counts as undecodable; an engine that
//     cannot be started, for the document or again for the pages after
//     it failed inside one, is no verdict on the document and gives no
//     text at all (errEngineStart);
//   - a document none of whose pages has usable text is refused: garbled
//     when some page was undecodable text, noText when there was none,
//     damaged when the engine could read no page at all (or there is none).
//
// Only the text of the pages is read: no form fields, annotations,
// bookmarks, metadata or embedded files, and nothing of the document is
// run (the engine is built without JavaScript). A PDF that needs no
// password to open is read whatever its permission flags say about
// copying, as pdf.js and pdftotext do; one that needs a password is
// refused, and no password is ever asked for.

// pdfDoc is the seam between the wrapper and the PDF engine: the only
// thing pdf_engine.go gives the rest of the package.
type pdfDoc interface {
	// NumPages is the number of pages in the document.
	NumPages() int
	// PageText is the text of page i (0-based) with "\n" line ends, at
	// most max bytes of it; cut reports that the page had more. The
	// error is errPDFPage for a page the engine cannot read, the
	// context's error once ctx (or the engine's own deadline) ended,
	// errEngineStart when a fresh engine for the pages left could not be
	// started, or any other error for an engine that cannot go on
	// (readerFailed).
	PageText(ctx context.Context, i, max int) (text string, cut bool, err error)
	// HiddenText reports whether a page read so far has text drawn
	// invisibly (the text layer of an OCR'd scan, or text hidden on
	// purpose), which PageText includes.
	HiddenText() bool
	// Close releases what the engine holds for the document.
	Close()
}

// The errors of the seam.
var (
	// errPDFEncrypted is what openPDF returns for a PDF that needs a user
	// password; one with an empty user password is opened.
	errPDFEncrypted = errors.New("pdf: a password is needed")
	// errPDFSecurity is what openPDF returns for a PDF encrypted with a
	// handler the engine does not support (a certificate, say).
	errPDFSecurity = errors.New("pdf: unsupported encryption")
	// errPDFDamaged is what openPDF returns when the engine finds no PDF
	// it can open in the bytes.
	errPDFDamaged = errors.New("pdf: not a PDF the engine can open")
	// errPDFPage is what PageText returns for a page the engine cannot
	// read.
	errPDFPage = errors.New("pdf: the page cannot be read")
)

// pdfHeader is how every PDF the reader reads starts. PDF readers accept
// the header anywhere in the first KiB; the bridge does not, so the check
// before a worker starts and this one agree.
var pdfHeader = []byte("%PDF-")

// pdfMarker is the line before the text of page n (1-based).
func pdfMarker(n int) string { return "--- page " + strconv.Itoa(n) + " ---" }

// pdfOpener opens a document with an engine: openPDF, or a test's fake.
type pdfOpener func(ctx context.Context, data []byte, lim Limits) (pdfDoc, error)

// extractPDF turns a PDF document into text.
func extractPDF(ctx context.Context, data []byte, lim Limits) (Result, error) {
	return extractPDFWith(ctx, data, lim, openPDF)
}

func extractPDFWith(ctx context.Context, data []byte, lim Limits, open pdfOpener) (Result, error) {
	if !bytes.HasPrefix(data, pdfHeader) {
		return Result{}, refuse(WrongFormat)
	}
	doc, err := open(ctx, data, lim)
	switch {
	case err == nil:
	case errors.Is(err, errPDFEncrypted):
		return Result{}, &Refusal{Code: Encrypted, What: WhatPassword}
	case errors.Is(err, errPDFSecurity):
		return Result{}, refuse(Encrypted)
	case errors.Is(err, errPDFDamaged):
		return Result{}, refuse(Damaged)
	default:
		// The context's error, the engine not starting (errEngineStart),
		// or the engine failing on the document (readerFailed).
		return Result{}, err
	}
	defer doc.Close()
	return readPDF(ctx, doc, lim)
}

// readPDF reads the pages of an open document.
func readPDF(ctx context.Context, doc pdfDoc, lim Limits) (Result, error) {
	var f Facts
	n := max(doc.NumPages(), 0)
	if n == 0 {
		return Result{}, refuse(Damaged)
	}
	f.Pages = min(n, MaxFact)
	toRead := min(n, lim.MaxPages)

	tb := newTextBuilder(lim.MaxTextBytes)
	usable, garbledPages, unreadable := 0, 0, 0
	for i := 0; i < toRead; i++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		text, cut, err := doc.PageText(ctx, i, lim.MaxPageBytes)
		switch {
		case err == nil:
		case errors.Is(err, errPDFPage):
			text, cut = "", false
			unreadable++
		default:
			// The context ended (the caller's, or the engine's own
			// deadline), or no fresh engine started for the pages left
			// (errEngineStart): no partial text, the reading is not
			// repeatable.
			if cerr := ctx.Err(); cerr != nil {
				return Result{}, cerr
			}
			return Result{}, err
		}
		f.PagesRead++
		text, cut = pageText(text, cut, lim.MaxPageBytes)
		if cut {
			f.PagesCut++
		}
		if err == nil {
			bad, nonSpace := countChars(text)
			switch {
			case garbled(bad, nonSpace, lim.MaxGarbledPercent):
				garbledPages++
				text = ""
			case nonSpace < minTextChars:
				f.PagesWithoutText++
			default:
				usable++
			}
		}
		tb.Blank()
		if !tb.Lines(pdfMarker(i+1)) || (text != "" && !tb.Lines(text)) {
			f.Cut, f.CutAt, f.CutPage = true, CutTextBytes, i+1
			break
		}
	}
	f.PagesUndecodable = garbledPages + unreadable
	f.HiddenContent = doc.HiddenText()

	if usable == 0 {
		switch {
		case garbledPages > 0:
			return Result{}, refuse(Garbled)
		case unreadable == f.PagesRead:
			return Result{}, refuse(Damaged)
		}
		return Result{}, refuse(NoText)
	}
	if !f.Cut && n > toRead {
		f.Cut, f.CutAt, f.CutPage = true, CutPages, toRead+1
	}
	return Result{Text: tb.String(), Facts: f}, nil
}

// pageText makes the engine's text of a page what the reader works on:
// valid UTF-8 with "\n" line ends (a lone CR would count as a bad
// character), at most maxBytes long, and, when the engine had more, cut
// at the end of its last whole line (inside the line only when the first
// line alone is longer than that); without blank lines at its start and
// end, so that a marker is followed by the page's first line.
func pageText(text string, cut bool, maxBytes int) (string, bool) {
	text = normalizeNewlines(strings.ToValidUTF8(text, string(utf8.RuneError)))
	if len(text) > maxBytes {
		text, cut = runePrefix(text, maxBytes), true
	}
	if cut {
		if nl := strings.LastIndexByte(text, '\n'); nl > 0 {
			text = text[:nl]
		}
	}
	return trimBlankLines(text), cut
}

// trimBlankLines drops the lines of nothing but white space at the start
// and the end of text.
func trimBlankLines(text string) string {
	for {
		line, rest, more := strings.Cut(text, "\n")
		if !more || strings.TrimSpace(line) != "" {
			break
		}
		text = rest
	}
	for {
		i := strings.LastIndexByte(text, '\n')
		if i < 0 || strings.TrimSpace(text[i+1:]) != "" {
			break
		}
		text = text[:i]
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}
