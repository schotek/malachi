// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
)

// No document parser runs in the bridge's process. get_attachment hands a
// PDF, DOCX or XLSX to a worker: this executable started again with
// extract.WorkerArg (main dispatches it before anything else), for that
// one document, with a memory watchdog and a deadline of its own, killed
// after workerTimeout. A parser fed a document built to attack it may
// panic, overflow its stack, exhaust memory or never return; in the
// bridge, any of that would end every tool of the client's session, in a
// worker it ends one call with a withheld result.
//
// Everything a worker answers is untrusted (extract.Run checks the reply
// against closed sets and bounds): its text goes through clean() and the
// fence like any mail text, and what is said about the document outside
// the fence is the bridge's own words over counts and codes. Nothing of a
// document reaches the log: only its format, sizes, page and sheet
// counts, the duration and the outcome.

// workerExecutable is the worker's executable: this one, as the operating
// system names it (not a PATH lookup). A variable, so that a test can make
// starting a worker fail.
var workerExecutable = os.Executable

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
	res     extract.Result
	refusal *extract.Refusal // with extract.Refused
	outcome extract.Outcome
	busy    bool // no worker was free in time; nothing ran
}

// extractDocument reads data, a document in format f, in a worker: it
// waits for a free slot (bounded by ctx and the pool's wait), then runs
// the worker under workerTimeout.
func (b *bridge) extractDocument(ctx context.Context, f extract.Format, data []byte) extraction {
	waitCtx, cancelWait := context.WithTimeout(ctx, b.workers.wait)
	select {
	case b.workers.slots <- struct{}{}:
		cancelWait()
	case <-waitCtx.Done():
		cancelWait()
		if ctx.Err() != nil {
			return extraction{outcome: extract.Cancelled}
		}
		b.log.Warn("document workers busy", "format", string(f), "inputBytes", len(data))
		return extraction{busy: true}
	}
	defer func() { <-b.workers.slots }()

	start := time.Now()
	x := extraction{outcome: extract.SpawnFailed}
	if exe, err := workerExecutable(); err == nil {
		runCtx, cancel := context.WithTimeout(ctx, workerTimeout)
		x.res, x.refusal, x.outcome = extract.Run(runCtx, exe, f, data, extract.DefaultLimits())
		cancel()
	}
	b.logExtraction(f, len(data), x, time.Since(start))
	return x
}

// logExtraction logs one extraction: counts and codes only, never the
// file's name, its text or anything the worker wrote.
func (b *bridge) logExtraction(f extract.Format, inputBytes int, x extraction, d time.Duration) {
	args := []any{
		"format", string(f), "inputBytes", inputBytes, "textBytes", len(x.res.Text),
		"pages", x.res.Facts.Pages, "sheets", x.res.Facts.Sheets,
		"dur", d.Round(time.Millisecond), "outcome", x.outcome.String(),
	}
	level := slog.LevelDebug
	switch x.outcome {
	case extract.OK, extract.Cancelled:
	case extract.Refused:
		args = append(args, "code", string(x.refusal.Code))
		if x.refusal.What != extract.WhatNone {
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
	case extract.OK, extract.Refused, extract.Crashed, extract.BadReply, extract.Memory:
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
	case extract.Timeout:
		return fmt.Sprintf("reading it took longer than %s", workerTimeout)
	case extract.Memory:
		return "reading it needed more than 1 GiB of memory"
	case extract.Crashed, extract.BadReply:
		return "the document reader stopped on this file (it may be damaged or built to attack readers)"
	case extract.SpawnFailed:
		return "the document reader could not be started"
	case extract.Mismatch:
		return "the bridge was updated while it ran; restart the Claude client"
	case extract.EngineFailed:
		// Only the PDF reader has an engine to start.
		return "the PDF reader could not be started; call again later"
	case extract.OK, extract.Refused, extract.Cancelled:
		return ""
	}
	return "the document reader stopped on this file (it may be damaged or built to attack readers)"
}

// formatName is how the bridge names a format in its own lines.
func formatName(f extract.Format) string {
	switch f {
	case extract.PDF:
		return "PDF"
	case extract.DOCX:
		return "Word document (.docx)"
	case extract.XLSX:
		return "Excel workbook (.xlsx)"
	}
	return "document"
}

// aFormat is formatName with its indefinite article.
func aFormat(f extract.Format) string {
	if f == extract.XLSX {
		return "an " + formatName(f)
	}
	return "a " + formatName(f)
}

// refusalReason is why a document the worker refused is withheld, in the
// bridge's words: a refusal is a code and a detail from closed sets, so
// nothing of the document is quoted.
func refusalReason(f extract.Format, r *extract.Refusal) string {
	switch r.Code {
	case extract.WrongFormat:
		switch r.What {
		case extract.WhatMacroEnabled:
			return "the file is a macro-enabled Office document (.docm, .xlsm), which is not read"
		case extract.WhatTemplate:
			return "the file is an Office template (.dotx, .xltx), which is not read"
		case extract.WhatPresentation:
			return "the file is a PowerPoint presentation, which is not read"
		case extract.WhatBinaryWorkbook:
			return "the file is a binary Excel workbook (.xlsb), which is not read"
		}
		return "the content is not " + aFormat(f)
	case extract.Encrypted:
		if f == extract.PDF {
			return "the PDF is protected with a password"
		}
		return "the document is protected with a password"
	case extract.OfficeCFB:
		return officeCFBReason
	case extract.Damaged:
		return "the " + formatName(f) + " is damaged and cannot be read"
	case extract.NoText:
		return "the PDF has no text layer (a scan or only pictures); the bridge does no OCR"
	case extract.Garbled:
		return "the PDF's text cannot be decoded (its fonts carry no character map)"
	case extract.TooBig:
		return fmt.Sprintf("too big: more than %d bytes", maxAttachmentDocumentBytes)
	case extract.Expands:
		lim := extract.DefaultLimits()
		return fmt.Sprintf("the document unpacks to more than its limit (%d MiB in one part, %d MiB in all)",
			lim.MaxEntryBytes>>20, lim.MaxExpandBytes>>20)
	case extract.TooManyParts:
		return fmt.Sprintf("the document has more than %d parts", extract.DefaultLimits().MaxZipEntries)
	case extract.TooDeep:
		return fmt.Sprintf("the document is nested more than %d levels deep", extract.DefaultLimits().MaxXMLDepth)
	case extract.TooManyTokens:
		return "the document has more XML than the bridge reads"
	case extract.Unsupported:
		switch r.What {
		case extract.WhatZipEncryption:
			return "a part of the document is encrypted"
		case extract.WhatCompressionMethod:
			return "the document is packed with a compression method the bridge does not read"
		case extract.WhatXMLEncoding:
			return "the document's XML is not UTF-8, which the bridge does not read"
		case extract.WhatDoctype:
			return "the document's XML declares a DOCTYPE, which is never read"
		}
		return "the " + formatName(f) + " uses something the bridge does not read"
	case extract.ReaderFailed:
		return "the document reader failed on this file (it may be damaged)"
	}
	return "the document could not be read"
}

// officeCFBReason is why an OLE2 compound file is withheld: an Office
// document protected with a password is one (its package is encrypted
// inside), and so are .doc and .xls.
const officeCFBReason = "the Office file is protected with a password, or in the older binary format (.doc, .xls), which is not read"
