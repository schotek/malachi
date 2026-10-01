// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package extract turns the bytes of an attached document (PDF, DOCX or
// XLSX) into plain text for malachi-mcp's get_attachment, and does nothing
// else: no MCP, no daemon calls, no logging, no files, no network.
//
// Only the bridge imports it; Go's internal rule keeps the daemon and the
// UI out, so extraction stays in the bridge. Even there, Extract must only
// ever run inside the worker: a child process of the bridge's own
// executable, started for one document (WorkerArg, ServeWorker), with a
// memory watchdog and its own deadline. Document parsers are fed hostile
// input, and a Go fatal error (a stack overflow, a runtime throw), an
// exhausted memory or a parser that never returns cannot be recovered
// from: in the bridge's process they would take down every tool of the
// client's session. The bridge talks to the worker through Run and trusts
// nothing it answers (worker.go; docs/mcp.md).
//
// Extraction is deterministic: the same bytes and limits give the same
// Result or the same Refusal, so a page offset into the text stays valid
// across calls. Volume caps cut the text at a line boundary and say so in
// Facts; structural caps refuse the document.
package extract

import (
	"context"
	"errors"
	"fmt"
)

// Format is a document format the package reads.
type Format string

// The formats. Anything else (older .doc and .xls, PowerPoint,
// OpenDocument, RTF) is not read.
const (
	PDF  Format = "pdf"
	DOCX Format = "docx"
	XLSX Format = "xlsx"
)

// Valid reports whether f is one of the formats the package reads.
func (f Format) Valid() bool {
	switch f {
	case PDF, DOCX, XLSX:
		return true
	}
	return false
}

// Limits bound what one extraction reads and returns (docs/mcp.md). The
// worker always applies DefaultLimits(): the protocol carries no limits,
// so a parent's own Limits only bound what it sends and what it accepts.
type Limits struct {
	MaxInputBytes int // document bytes; == api.MaxAttachmentDataBytes (test-asserted by the bridge)
	MaxTextBytes  int // text after normalisation; more is cut at a line boundary

	MaxPages     int // PDF pages read; more are cut
	MaxPageBytes int // text of one PDF page; more is cut and noted

	MaxZipEntries  int   // OOXML central-directory entries; more is refused (tooManyParts)
	MaxEntryBytes  int64 // decompressed bytes of one part read; more is refused (expands)
	MaxExpandBytes int64 // decompressed bytes of all parts read; more is refused (expands)

	MaxXMLDepth   int // element nesting; deeper is refused (tooDeep)
	MaxPartTokens int // XML tokens of one part; more is refused (tooManyTokens)
	MaxTokens     int // XML tokens of all parts; more is refused (tooManyTokens)

	MaxSheets        int // XLSX sheets read; more are cut
	MaxRowsPerSheet  int // non-empty rows of one sheet; more are cut
	MaxCells         int // non-empty cells of the workbook; more are cut
	MaxColumns       int // columns of one row; more are dropped and counted
	MaxSharedStrings int // shared strings kept in memory

	MaxNotes          int // DOCX comments, footnotes and endnotes together; more are cut
	MaxHeadersFooters int // DOCX header and footer parts read; more are left out
	MaxTableDepth     int // DOCX table nesting; deeper tables are flattened

	// MaxGarbledPercent is how much of a PDF page's non-space characters
	// may be undecodable before the page is dropped; == the bridge's
	// maxReplacedPercent (test-asserted by the bridge).
	MaxGarbledPercent int
}

// DefaultLimits are the limits of docs/mcp.md, the ones the worker applies.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 16 << 20,
		MaxTextBytes:  1 << 20,

		MaxPages:     500,
		MaxPageBytes: 256 << 10,

		MaxZipEntries:  2000,
		MaxEntryBytes:  64 << 20,
		MaxExpandBytes: 128 << 20,

		MaxXMLDepth:   128,
		MaxPartTokens: 8_000_000,
		MaxTokens:     16_000_000,

		MaxSheets:        256,
		MaxRowsPerSheet:  100_000,
		MaxCells:         500_000,
		MaxColumns:       256,
		MaxSharedStrings: 1_000_000,

		MaxNotes:          10_000,
		MaxHeadersFooters: 64,
		MaxTableDepth:     16,

		MaxGarbledPercent: 10,
	}
}

// Code is why a document is refused, from a closed set the bridge words
// itself; nothing the document says reaches the model through it.
type Code string

// The refusal codes.
const (
	WrongFormat   Code = "wrongFormat"   // the bytes are not the declared format (What says what they are, when known)
	Encrypted     Code = "encrypted"     // a password is needed (What: password)
	OfficeCFB     Code = "officeCFB"     // an OLE2 compound file: a password-protected OOXML file or an older .doc/.xls
	Damaged       Code = "damaged"       // the container or its structure is broken, or the input is empty
	NoText        Code = "noText"        // a PDF without a text layer (a scan, only pictures)
	Garbled       Code = "garbled"       // a PDF whose text cannot be decoded (fonts without a character map)
	TooBig        Code = "tooBig"        // more input than MaxInputBytes
	Expands       Code = "expands"       // decompresses to more than MaxEntryBytes or MaxExpandBytes
	TooManyParts  Code = "tooManyParts"  // more ZIP entries than MaxZipEntries
	TooDeep       Code = "tooDeep"       // nested deeper than MaxXMLDepth
	TooManyTokens Code = "tooManyTokens" // more XML tokens than MaxPartTokens or MaxTokens
	Unsupported   Code = "unsupported"   // a feature the package does not read (What says which)
	ReaderFailed  Code = "readerFailed"  // the reader itself failed (a recovered panic, a broken result)
)

// codes is the closed set of Code.
var codes = map[Code]bool{
	WrongFormat: true, Encrypted: true, OfficeCFB: true, Damaged: true,
	NoText: true, Garbled: true, TooBig: true, Expands: true,
	TooManyParts: true, TooDeep: true, TooManyTokens: true,
	Unsupported: true, ReaderFailed: true,
}

// Known reports whether c is one of the refusal codes.
func (c Code) Known() bool { return codes[c] }

// The details of a refusal, a closed set like Code: the bridge words them,
// so the reason a document is refused never quotes the document. WhatNone
// is a refusal that needs no detail.
const (
	WhatNone              = ""
	WhatZipEncryption     = "zipEncryption"     // unsupported: a ZIP entry is encrypted
	WhatCompressionMethod = "compressionMethod" // unsupported: a ZIP method other than store and deflate
	WhatXMLEncoding       = "xmlEncoding"       // unsupported: XML not in UTF-8
	WhatDoctype           = "doctype"           // unsupported: XML with a DOCTYPE
	WhatMacroEnabled      = "macroEnabled"      // wrongFormat: .docm, .xlsm
	WhatTemplate          = "template"          // wrongFormat: .dotx, .xltx (and their macro-enabled forms)
	WhatPresentation      = "presentation"      // wrongFormat: a PowerPoint file
	WhatBinaryWorkbook    = "binaryWorkbook"    // wrongFormat: .xlsb
	WhatOtherZip          = "otherZip"          // wrongFormat: a ZIP that is not the declared OOXML document
	WhatPassword          = "password"          // encrypted: a PDF that needs a user password
)

// whats is the closed set of Refusal.What.
var whats = map[string]bool{
	WhatNone: true, WhatZipEncryption: true, WhatCompressionMethod: true,
	WhatXMLEncoding: true, WhatDoctype: true, WhatMacroEnabled: true,
	WhatTemplate: true, WhatPresentation: true, WhatBinaryWorkbook: true,
	WhatOtherZip: true, WhatPassword: true,
}

// KnownWhat reports whether w is one of the refusal details.
func KnownWhat(w string) bool { return whats[w] }

// Refusal is a document the package does not turn into text, and why.
// Code and What come from closed sets, so the error text is safe to log.
type Refusal struct {
	Code Code
	What string
}

func (r *Refusal) Error() string {
	if r.What == WhatNone {
		return "extract: refused: " + string(r.Code)
	}
	return "extract: refused: " + string(r.Code) + " (" + r.What + ")"
}

// Valid reports whether the code and the detail are from their closed sets.
func (r *Refusal) Valid() bool { return r.Code.Known() && KnownWhat(r.What) }

// refuse is a refusal with no detail.
func refuse(c Code) *Refusal { return &Refusal{Code: c} }

// MaxFact is the largest value of a count in Facts. A reader saturates a
// count there rather than overflow it; a reply with a larger one is broken.
const MaxFact = 1 << 24

// Where a cut happened (Facts.CutAt), a closed set.
const (
	CutNone      = ""
	CutTextBytes = "textBytes" // the text reached MaxTextBytes
	CutPages     = "pages"     // MaxPages
	CutSheets    = "sheets"    // MaxSheets
	CutRows      = "rows"      // MaxRowsPerSheet
	CutCells     = "cells"     // MaxCells
	CutNotes     = "notes"     // MaxNotes
)

var cutPlaces = map[string]bool{
	CutNone: true, CutTextBytes: true, CutPages: true, CutSheets: true,
	CutRows: true, CutCells: true, CutNotes: true,
}

// Facts are what the bridge says about the text outside the fence: counts
// and flags only, worded by the bridge. Each format fills its own fields
// and leaves the others zero.
type Facts struct {
	// PDF.
	Pages            int `json:"pages"`            // pages in the document
	PagesRead        int `json:"pagesRead"`        // pages read (at most MaxPages)
	PagesWithoutText int `json:"pagesWithoutText"` // pages read with no text (scans, pictures)
	PagesUndecodable int `json:"pagesUndecodable"` // pages read whose text could not be decoded, left out
	PagesCut         int `json:"pagesCut"`         // pages whose text was cut at MaxPageBytes

	// XLSX.
	Sheets          int `json:"sheets"`          // worksheets in the workbook
	SheetsHidden    int `json:"sheetsHidden"`    // of them hidden or very hidden (read, marked)
	SheetsRead      int `json:"sheetsRead"`      // worksheets read
	SheetsSkipped   int `json:"sheetsSkipped"`   // chartsheets and dialogsheets, not read
	Rows            int `json:"rows"`            // non-empty rows read
	ColumnsDropped  int `json:"columnsDropped"`  // cells beyond MaxColumns in their row, left out
	Formulas        int `json:"formulas"`        // cells showing a formula's last calculated value
	Uncalculated    int `json:"uncalculated"`    // formula cells never calculated, shown empty
	CellsUnresolved int `json:"cellsUnresolved"` // cells naming a shared string that does not exist, shown empty

	// DOCX (Comments also counts XLSX cell comments).
	Comments       int `json:"comments"`
	Footnotes      int `json:"footnotes"`
	Endnotes       int `json:"endnotes"`
	HeadersFooters int `json:"headersFooters"` // distinct headers and footers included

	TrackedChanges bool `json:"trackedChanges"` // DOCX: shown as accepted, deleted text left out
	HiddenContent  bool `json:"hiddenContent"`  // hidden text, sheets, rows or columns, or invisible PDF text, included

	// Cut is true when a volume cap left text out; CutAt says which cap
	// came first, and CutPage, CutSheet and CutRow (1-based, 0 when not
	// applicable) where the text that is left out begins.
	Cut      bool   `json:"cut"`
	CutAt    string `json:"cutAt"`
	CutPage  int    `json:"cutPage"`
	CutSheet int    `json:"cutSheet"`
	CutRow   int    `json:"cutRow"`
}

// counts lists every int field of Facts with its JSON name, so that a
// bound check cannot miss one (a test holds it to the struct).
func (f *Facts) counts() []struct {
	name string
	v    int
} {
	return []struct {
		name string
		v    int
	}{
		{"pages", f.Pages}, {"pagesRead", f.PagesRead},
		{"pagesWithoutText", f.PagesWithoutText}, {"pagesUndecodable", f.PagesUndecodable},
		{"pagesCut", f.PagesCut},
		{"sheets", f.Sheets}, {"sheetsHidden", f.SheetsHidden}, {"sheetsRead", f.SheetsRead},
		{"sheetsSkipped", f.SheetsSkipped}, {"rows", f.Rows}, {"columnsDropped", f.ColumnsDropped},
		{"formulas", f.Formulas}, {"uncalculated", f.Uncalculated}, {"cellsUnresolved", f.CellsUnresolved},
		{"comments", f.Comments}, {"footnotes", f.Footnotes}, {"endnotes", f.Endnotes},
		{"headersFooters", f.HeadersFooters},
		{"cutPage", f.CutPage}, {"cutSheet", f.CutSheet}, {"cutRow", f.CutRow},
	}
}

// Validate checks the facts against the protocol's bounds: every count in
// 0..MaxFact, CutAt from its closed set and set exactly when Cut is. The
// worker checks a result before it replies, the parent every reply.
func (f Facts) Validate() error {
	for _, c := range f.counts() {
		if c.v < 0 || c.v > MaxFact {
			return fmt.Errorf("fact %s out of range: %d", c.name, c.v)
		}
	}
	if !cutPlaces[f.CutAt] {
		return errors.New("fact cutAt not a known place")
	}
	if f.Cut != (f.CutAt != CutNone) {
		return errors.New("facts cut and cutAt disagree")
	}
	return nil
}

// Result is the text of a document and what the bridge says about it.
// Text is valid UTF-8 with "\n" line ends, at most MaxTextBytes long.
type Result struct {
	Text  string
	Facts Facts
}

// errEngineStart is a reader's engine that could not be started (its
// runtime or module, at the document's start or again after it failed
// inside a page): the machine's doing (memory, an address-space limit,
// executable memory refused), not the document's, and it may not happen
// on the next call. It is no verdict on the document, so the worker gives
// no reply for it but an exit status of its own (worker.go), which the
// bridge neither words as a damaged file nor caches. Only the PDF reader
// has an engine to start.
var errEngineStart = errors.New("extract: the reader's engine could not be started")

// Extract turns data in format f into text. The error is a *Refusal,
// ctx's error when ctx ended before the reader finished, or errEngineStart
// when the reader's engine could not be started; a reader's other errors
// become a readerFailed refusal. It does not recover panics: the worker
// does (ServeWorker), and fuzzing should see them.
//
// Deterministic: the same bytes and limits give the same Result or the
// same Refusal (an engine that does not start is not the bytes' doing).
func Extract(ctx context.Context, f Format, data []byte, lim Limits) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(data) > lim.MaxInputBytes {
		return Result{}, refuse(TooBig)
	}
	if len(data) == 0 {
		return Result{}, refuse(Damaged)
	}
	var (
		res Result
		err error
	)
	switch f {
	case PDF:
		res, err = extractPDF(ctx, data, lim)
	case DOCX:
		res, err = extractDOCX(ctx, data, lim)
	case XLSX:
		res, err = extractXLSX(ctx, data, lim)
	default:
		return Result{}, refuse(Unsupported)
	}
	if err == nil {
		return res, nil
	}
	if cerr := ctx.Err(); cerr != nil {
		return Result{}, cerr
	}
	if errors.Is(err, errEngineStart) {
		return Result{}, errEngineStart
	}
	var r *Refusal
	if errors.As(err, &r) && r.Valid() {
		return Result{}, r
	}
	return Result{}, refuse(ReaderFailed)
}
