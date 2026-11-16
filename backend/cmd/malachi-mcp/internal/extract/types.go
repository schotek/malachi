// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import "github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/doctext"

// The types of the worker's reply are package doctext's, which the
// bridge's server imports without the readers; these aliases keep their
// names here.
type (
	Format  = doctext.Format
	Limits  = doctext.Limits
	Code    = doctext.Code
	Refusal = doctext.Refusal
	Facts   = doctext.Facts
	Result  = doctext.Result
	Outcome = doctext.Outcome
)

// The formats.
const (
	PDF  = doctext.PDF
	DOCX = doctext.DOCX
	XLSX = doctext.XLSX
)

// DefaultLimits are the limits of docs/mcp.md, the ones the worker applies.
func DefaultLimits() Limits { return doctext.DefaultLimits() }

// The refusal codes.
const (
	WrongFormat   = doctext.WrongFormat
	Encrypted     = doctext.Encrypted
	OfficeCFB     = doctext.OfficeCFB
	Damaged       = doctext.Damaged
	NoText        = doctext.NoText
	Garbled       = doctext.Garbled
	TooBig        = doctext.TooBig
	Expands       = doctext.Expands
	TooManyParts  = doctext.TooManyParts
	TooDeep       = doctext.TooDeep
	TooManyTokens = doctext.TooManyTokens
	Unsupported   = doctext.Unsupported
	ReaderFailed  = doctext.ReaderFailed
)

// The details of a refusal.
const (
	WhatNone              = doctext.WhatNone
	WhatZipEncryption     = doctext.WhatZipEncryption
	WhatCompressionMethod = doctext.WhatCompressionMethod
	WhatXMLEncoding       = doctext.WhatXMLEncoding
	WhatDoctype           = doctext.WhatDoctype
	WhatMacroEnabled      = doctext.WhatMacroEnabled
	WhatTemplate          = doctext.WhatTemplate
	WhatPresentation      = doctext.WhatPresentation
	WhatBinaryWorkbook    = doctext.WhatBinaryWorkbook
	WhatOtherZip          = doctext.WhatOtherZip
	WhatPassword          = doctext.WhatPassword
)

// KnownWhat reports whether w is one of the refusal details.
func KnownWhat(w string) bool { return doctext.KnownWhat(w) }

// MaxFact is the largest value of a count in Facts.
const MaxFact = doctext.MaxFact

// Where a cut happened (Facts.CutAt).
const (
	CutNone      = doctext.CutNone
	CutTextBytes = doctext.CutTextBytes
	CutPages     = doctext.CutPages
	CutSheets    = doctext.CutSheets
	CutRows      = doctext.CutRows
	CutCells     = doctext.CutCells
	CutNotes     = doctext.CutNotes
)

// The outcomes of Run.
const (
	OK           = doctext.OK
	Refused      = doctext.Refused
	Timeout      = doctext.Timeout
	Memory       = doctext.Memory
	Crashed      = doctext.Crashed
	BadReply     = doctext.BadReply
	SpawnFailed  = doctext.SpawnFailed
	Cancelled    = doctext.Cancelled
	Mismatch     = doctext.Mismatch
	EngineFailed = doctext.EngineFailed
)
