// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mcpbridge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/doctext"
)

// No document parser runs in the bridge's process. get_attachment hands a
// PDF, DOCX or XLSX to the Extractor of Options.Extract: in malachi-mcp a
// worker, the executable started again with extract.WorkerArg for that
// one document, with a memory watchdog and a deadline of its own, killed
// after workerTimeout. A parser fed a document built to attack it may
// panic, overflow its stack, exhaust memory or never return; in the
// bridge, any of that would end every tool of the client's session, in a
// worker it ends one call with a withheld result. Without an Extractor
// (the bridge served in a process that cannot start one) no document is
// read: getAttachment withholds it before fetching a byte.
//
// Everything a worker answers is untrusted (extract.Run checks the reply
// against closed sets and bounds): its text goes through clean() and the
// fence like any mail text, and what is said about the document outside
// the fence is the bridge's own words over counts and codes. Nothing of a
// document reaches the log: only its format, sizes, page and sheet
// counts, the duration and the outcome.

// documentReaderUnavailable is why a document is withheld by a bridge
// without an Extractor.
const documentReaderUnavailable = "this Malachi Mail has no document reader, so the text of PDF, Word and Excel attachments is not read here"

// workerPool bounds how many workers run at once.
type workerPool struct {
	slots chan struct{}
	wait  time.Duration // how long a call waits for a free slot
}

func newWorkerPool() *workerPool {
	return &workerPool{slots: make(chan struct{}, maxConcurrentWorkers), wait: workerWaitTimeout}
}

// extraction is how the reading of one document ended.
type extraction struct {
	res     doctext.Result
	refusal *doctext.Refusal // with doctext.Refused
	outcome doctext.Outcome
	busy    bool // no worker was free in time; nothing ran
}

// extractDocument reads data, a document in format f, in a worker: it
// waits for a free slot (bounded by ctx and the pool's wait), then runs
// the worker under workerTimeout.
func (b *bridge) extractDocument(ctx context.Context, f doctext.Format, data []byte) extraction {
	waitCtx, cancelWait := context.WithTimeout(ctx, b.workers.wait)
	select {
	case b.workers.slots <- struct{}{}:
		cancelWait()
	case <-waitCtx.Done():
		cancelWait()
		if ctx.Err() != nil {
			return extraction{outcome: doctext.Cancelled}
		}
		b.log.Warn("document workers busy", "format", string(f), "inputBytes", len(data))
		return extraction{busy: true}
	}
	defer func() { <-b.workers.slots }()

	start := time.Now()
	x := extraction{outcome: doctext.SpawnFailed}
	if b.extract != nil {
		runCtx, cancel := context.WithTimeout(ctx, workerTimeout)
		x.res, x.refusal, x.outcome = b.extract(runCtx, f, data)
		cancel()
	}
	b.logExtraction(f, len(data), x, time.Since(start))
	return x
}

// logExtraction logs one extraction: counts and codes only, never the
// file's name, its text or anything the worker wrote.
func (b *bridge) logExtraction(f doctext.Format, inputBytes int, x extraction, d time.Duration) {
	args := []any{
		"format", string(f), "inputBytes", inputBytes, "textBytes", len(x.res.Text),
		"pages", x.res.Facts.Pages, "sheets", x.res.Facts.Sheets,
		"dur", d.Round(time.Millisecond), "outcome", x.outcome.String(),
	}
	level := slog.LevelDebug
	switch x.outcome {
	case doctext.OK, doctext.Cancelled:
	case doctext.Refused:
		args = append(args, "code", string(x.refusal.Code))
		if x.refusal.What != doctext.WhatNone {
			args = append(args, "what", x.refusal.What)
		}
	default:
		level = slog.LevelWarn
	}
	b.log.Log(context.Background(), level, "document extracted", args...)
}

// cacheable reports whether an extraction comes out the same for the same
// bytes, and may be cached (doc_cache.go).
func (x extraction) cacheable() bool {
	if x.busy {
		return false
	}
	switch x.outcome {
	case doctext.OK, doctext.Refused, doctext.Crashed, doctext.BadReply, doctext.Memory:
		return true
	}
	// Timeout, SpawnFailed, Cancelled, Mismatch, EngineFailed: the
	// moment's, not the document's.
	return false
}

// workerFailureReason is why a document is withheld when its worker did
// not reply with a text or a refusal; "" for those two outcomes and a
// cancelled call, which is a tool error.
func workerFailureReason(x extraction) string {
	if x.busy {
		return "the document reader is busy with other attachments; call again"
	}
	switch x.outcome {
	case doctext.Timeout:
		return fmt.Sprintf("reading it took longer than %s", workerTimeout)
	case doctext.Memory:
		return "reading it needed more than 1 GiB of memory"
	case doctext.Crashed, doctext.BadReply:
		return "the document reader stopped on this file (it may be damaged or built to attack readers)"
	case doctext.SpawnFailed:
		return "the document reader could not be started"
	case doctext.Mismatch:
		return "the bridge was updated while it ran; restart the Claude client"
	case doctext.EngineFailed:
		// Only the PDF reader has an engine to start.
		return "the PDF reader could not be started; call again later"
	case doctext.OK, doctext.Refused, doctext.Cancelled:
		return ""
	}
	return "the document reader stopped on this file (it may be damaged or built to attack readers)"
}

// formatName is how the bridge names a format in its own lines.
func formatName(f doctext.Format) string {
	switch f {
	case doctext.PDF:
		return "PDF"
	case doctext.DOCX:
		return "Word document (.docx)"
	case doctext.XLSX:
		return "Excel workbook (.xlsx)"
	}
	return "document"
}

// aFormat is formatName with its indefinite article.
func aFormat(f doctext.Format) string {
	if f == doctext.XLSX {
		return "an " + formatName(f)
	}
	return "a " + formatName(f)
}

// refusalReason is why a document the worker refused is withheld, in the
// bridge's words: a refusal is a code and a detail from closed sets, so
// nothing of the document is quoted.
func refusalReason(f doctext.Format, r *doctext.Refusal) string {
	switch r.Code {
	case doctext.WrongFormat:
		switch r.What {
		case doctext.WhatMacroEnabled:
			return "the file is a macro-enabled Office document (.docm, .xlsm), which is not read"
		case doctext.WhatTemplate:
			return "the file is an Office template (.dotx, .xltx), which is not read"
		case doctext.WhatPresentation:
			return "the file is a PowerPoint presentation, which is not read"
		case doctext.WhatBinaryWorkbook:
			return "the file is a binary Excel workbook (.xlsb), which is not read"
		}
		return "the content is not " + aFormat(f)
	case doctext.Encrypted:
		if f == doctext.PDF {
			return "the PDF is protected with a password"
		}
		return "the document is protected with a password"
	case doctext.OfficeCFB:
		return officeCFBReason
	case doctext.Damaged:
		return "the " + formatName(f) + " is damaged and cannot be read"
	case doctext.NoText:
		return "the PDF has no text layer (a scan or only pictures); the bridge does no OCR"
	case doctext.Garbled:
		return "the PDF's text cannot be decoded (its fonts carry no character map)"
	case doctext.TooBig:
		return fmt.Sprintf("too big: more than %d bytes", maxAttachmentDocumentBytes)
	case doctext.Expands:
		lim := doctext.DefaultLimits()
		return fmt.Sprintf("the document unpacks to more than its limit (%d MiB in one part, %d MiB in all)",
			lim.MaxEntryBytes>>20, lim.MaxExpandBytes>>20)
	case doctext.TooManyParts:
		return fmt.Sprintf("the document has more than %d parts", doctext.DefaultLimits().MaxZipEntries)
	case doctext.TooDeep:
		return fmt.Sprintf("the document is nested more than %d levels deep", doctext.DefaultLimits().MaxXMLDepth)
	case doctext.TooManyTokens:
		return "the document has more XML than the bridge reads"
	case doctext.Unsupported:
		switch r.What {
		case doctext.WhatZipEncryption:
			return "a part of the document is encrypted"
		case doctext.WhatCompressionMethod:
			return "the document is packed with a compression method the bridge does not read"
		case doctext.WhatXMLEncoding:
			return "the document's XML is not UTF-8, which the bridge does not read"
		case doctext.WhatDoctype:
			return "the document's XML declares a DOCTYPE, which is never read"
		}
		return "the " + formatName(f) + " uses something the bridge does not read"
	case doctext.ReaderFailed:
		return "the document reader failed on this file (it may be damaged)"
	}
	return "the document could not be read"
}

// officeCFBReason is why an OLE2 compound file is withheld: an Office
// document protected with a password is one (its package is encrypted
// inside), and so are .doc and .xls.
const officeCFBReason = "the Office file is protected with a password, or in the older binary format (.doc, .xls), which is not read"
