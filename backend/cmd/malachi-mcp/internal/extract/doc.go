// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package extract turns the bytes of an attached document (PDF, DOCX or
// XLSX) into plain text for malachi-mcp's get_attachment, and does nothing
// else: no MCP, no daemon calls, no logging, no files, no network.
//
// Only the bridge's executable imports it; Go's internal rule keeps the
// daemon and the UI out, and the bridge's server (package mcpbridge) knows
// only the types of package doctext, which this package's types are
// aliases of, so a program that serves the bridge without this executable
// links no parser. Even in the executable, Extract must only
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
)

// refuse is a refusal with no detail.
func refuse(c Code) *Refusal { return &Refusal{Code: c} }

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
