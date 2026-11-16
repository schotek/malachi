// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package doctext is what malachi-mcp's get_attachment and its document
// worker say about a document's text: the formats read, the limits, the
// refusals and facts of the worker's reply, and how one reading ended.
// It holds no reader: the parsers (and PDFium) are in package extract,
// which only the bridge's executable imports and runs in its worker
// process, so that a program serving the bridge without that executable
// links no document parser.
package doctext

import (
	"errors"
	"fmt"
	"strconv"
)

// Format is a document format the worker reads.
type Format string

// The formats. Anything else (older .doc and .xls, PowerPoint,
// OpenDocument, RTF) is not read.
const (
	PDF  Format = "pdf"
	DOCX Format = "docx"
	XLSX Format = "xlsx"
)

// Valid reports whether f is one of the formats the worker reads.
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
	Unsupported   Code = "unsupported"   // a feature the worker does not read (What says which)
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

// Refusal is a document the worker does not turn into text, and why.
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

// Outcome is how one reading of a document in a worker (extract.Run)
// ended.
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
