// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
)

// The PDF engine: PDFium compiled to WebAssembly (go-pdfium's webassembly
// build, embedded in the binary) and run by wazero, pure Go without cgo
// (docs/architecture.md §7). This is the only file of the package that
// imports a PDF library; the rest sees a pdfDoc.
//
// PDFium is C++ and is fed hostile documents, so it runs boxed even inside
// the worker process: its memory is a wasm linear memory with a fixed
// ceiling, it reaches no host file (nothing is mounted), its output
// streams go nowhere, and a call that runs past the document's deadline
// is stopped by wazero closing the module. Every document gets a runtime
// and a module instance of its own; only the compiled code is shared
// within a process.

// The engine's limits.
const (
	// pdfMemoryPages is the ceiling of PDFium's linear memory in 64 KiB
	// wasm pages (256 MiB; an ordinary document needs about 20 MiB). A
	// page needing more fails inside the module, which counts it as a
	// page that cannot be read. With what a memory may briefly hold twice
	// (pdfMemoryEager) it stays under the worker's soft limit (worker.go),
	// so the Go heap keeps room beside it.
	pdfMemoryPages = 4096

	// pdfMemoryEager is how far the linear memory grows by doubling, each
	// growth a copy (reservedMemory); past it the whole ceiling is
	// allocated at once.
	pdfMemoryEager = 64 << 20

	// pdfDocumentDeadline bounds one document, the engine's start
	// included, when the caller's context has no earlier deadline; at the
	// deadline wazero stops the module. The worker's context ends sooner
	// (workerReaderDeadline), so in the worker this only backs it up.
	pdfDocumentDeadline = 28 * time.Second

	// pdfCompileWorkers compile the module in parallel at the start of a
	// worker: the compile is most of a worker's cold start.
	pdfCompileWorkers = 4

	// pdfTextChunk is how many characters one FPDFText_GetText call asks
	// for: a page is read in chunks until it has more than it may give.
	pdfTextChunk = 16 << 10

	// pdfObjectsPerPage and pdfObjectsPerDocument bound the page objects
	// looked at for invisible text (Facts.HiddenContent): each one costs
	// about 4 µs and a handle that go-pdfium keeps until the document is
	// closed. An OCR'd page has its invisible text among its first objects.
	pdfObjectsPerPage     = 2048
	pdfObjectsPerDocument = 32768

	// pdfRestarts is how many times one document may get a fresh module
	// after the engine failed inside a page (a trap, an exhausted memory);
	// past that, the pages that are left are not read.
	pdfRestarts = 8
)

// pdfRandSeed seeds the random source the module would see. The module
// imports none today (no WASI random_get), and PDFium needs no randomness
// to read a document; should a build import one, a fixed seed keeps a
// document's reading the same each time.
var pdfRandSeed = [32]byte{'m', 'a', 'l', 'a', 'c', 'h', 'i', '-', 'p', 'd', 'f'}

// pdfCache shares the compiled module between the documents of one
// process (in memory only: compiled code on disk would be code a writable
// directory could replace).
var (
	pdfCacheOnce sync.Once
	pdfCache     wazero.CompilationCache
	// pdfCompiled is set once the module has been compiled in this
	// process.
	pdfCompiled atomic.Bool
)

func pdfCompilationCache() wazero.CompilationCache {
	pdfCacheOnce.Do(func() { pdfCache = wazero.NewCompilationCache() })
	return pdfCache
}

// pdfiumNoObject is go-pdfium's error for a NULL page object, which it
// has no sentinel for.
const pdfiumNoObject = "could not get object"

// pdfiumLineHyphen is what PDFium gives for a hyphen at the end of a line
// whose word goes on in the next line (the two lines joined): a
// noncharacter, read as the hyphen it stands for. The word stays
// hyphenated, since PDFium cannot tell a hyphenated compound from a word
// broken in two.
const pdfiumLineHyphen = 0xFFFE

// errPDFEngine is the engine failing inside a call: a trap, an exhausted
// memory, a panic in the glue. The module may be broken afterwards.
var errPDFEngine = errors.New("pdf: the engine failed")

// pdfiumInit starts a runtime with the module compiled (or taken from
// pdfCache). A variable, so that a test can make the engine fail to start.
var pdfiumInit = webassembly.Init

// openPDF opens a PDF document with the engine.
func openPDF(ctx context.Context, data []byte, _ Limits) (pdfDoc, error) {
	dctx, cancel := context.WithTimeout(ctx, pdfDocumentDeadline)
	d := &pdfiumDoc{ctx: dctx, cancel: cancel, data: data}
	if err := d.open(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// pdfiumDoc is one document open in PDFium, with the runtime and the
// module instance it alone uses.
type pdfiumDoc struct {
	ctx    context.Context
	cancel context.CancelFunc
	data   []byte

	pool  pdfium.Pool
	inst  pdfium.Pdfium
	doc   references.FPDF_DOCUMENT
	pages int

	// broken is set when a call failed in a way that may have left the
	// module inconsistent; the next page starts a fresh one.
	broken   bool
	restarts int

	hidden  bool // a text object with invisible text was seen
	objects int  // page objects looked at for it
}

// pdfiumConfig is how every document's runtime is made. ctx is the
// context every call of the module runs under: when it ends, wazero stops
// the call (WithCloseOnContextDone). It also carries the memory allocator
// and the compile workers.
func pdfiumConfig(ctx context.Context) webassembly.Config {
	ctx = experimental.WithMemoryAllocator(ctx, experimental.MemoryAllocatorFunc(newReservedMemory))
	ctx = experimental.WithCompilationWorkers(ctx, pdfCompileWorkers)
	return webassembly.Config{
		Context: ctx,
		// One instance, made when the document asks for it, never reused.
		MinIdle:  0,
		MaxIdle:  1,
		MaxTotal: 1,
		// Nothing of the host's file system is mounted (go-pdfium's
		// default mounts the root): PDFium opens no file.
		FSConfig: wazero.NewFSConfig(),
		// The compiler where wazero has one (amd64 and arm64), else the
		// interpreter.
		RuntimeConfig: wazero.NewRuntimeConfig().
			WithCloseOnContextDone(true).
			WithMemoryLimitPages(pdfMemoryPages).
			WithCompilationCache(pdfCompilationCache()),
		Stdout:       io.Discard,
		Stderr:       io.Discard,
		RandomSource: rand.NewChaCha8(pdfRandSeed),
	}
}

// open starts a runtime and a module instance and opens the document in
// it. On an error the caller closes d. A runtime or an instance that does
// not start (the compile, its executable memory, the instance's memory)
// is errEngineStart: nothing of the document was read yet.
func (d *pdfiumDoc) open() error {
	pool, err := pdfiumInit(pdfiumConfig(d.ctx))
	if err != nil {
		return d.failStart(err)
	}
	d.pool = pool
	if pdfCompiled.CompareAndSwap(false, true) {
		// The compile leaves a few hundred MiB of garbage behind; given
		// back now, before PDFium runs, it is not resident beside
		// PDFium's memory (5 ms).
		debug.FreeOSMemory()
	}
	inst, err := pool.GetInstanceWithContext(d.ctx)
	if err != nil {
		return d.failStart(err)
	}
	d.inst = inst
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &d.data})
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword):
		return errPDFEncrypted
	case errors.Is(err, pdfiumerrors.ErrSecurity):
		return errPDFSecurity
	case errors.Is(err, pdfiumerrors.ErrFormat), errors.Is(err, pdfiumerrors.ErrFile),
		errors.Is(err, pdfiumerrors.ErrUnknown), errors.Is(err, pdfiumerrors.ErrPage):
		return errPDFDamaged
	case err != nil:
		return d.fail(err)
	}
	d.doc = doc.Document
	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: d.doc})
	if err != nil {
		return d.fail(err)
	}
	d.pages = max(count.PageCount, 0)
	return nil
}

// fail is the error for a call that failed: the context's when the
// document's deadline (or the caller's context) ended it, else
// errPDFEngine; either way the module is not used again.
func (d *pdfiumDoc) fail(err error) error {
	d.broken = true
	if cerr := d.ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%w: %w", errPDFEngine, err)
}

// failStart is fail for a runtime or a module instance that did not
// start: the context's error when it ended, else errEngineStart.
func (d *pdfiumDoc) failStart(err error) error {
	d.broken = true
	if cerr := d.ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%w: %w", errEngineStart, err)
}

func (d *pdfiumDoc) NumPages() int { return d.pages }

// restart replaces a broken module with a fresh one holding the same
// document. The same bytes opened in a fresh module before, so a module
// that does not start or open them now is the machine's doing:
// errEngineStart, whatever step failed, and the pages left are not read.
func (d *pdfiumDoc) restart() error {
	if d.restarts >= pdfRestarts {
		return errPDFPage
	}
	d.restarts++
	d.shut()
	d.broken = false
	if err := d.open(); err != nil {
		if d.ctx.Err() != nil {
			return d.ctx.Err()
		}
		d.broken = true
		return fmt.Errorf("%w: opening the document again: %w", errEngineStart, err)
	}
	return nil
}

func (d *pdfiumDoc) PageText(ctx context.Context, i, maxBytes int) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if d.broken {
		if err := d.restart(); err != nil {
			return "", false, err
		}
	}
	text, cut, err := d.pageText(i, maxBytes)
	switch {
	case err == nil:
		return text, cut, nil
	case errors.Is(err, errPDFPage):
		return "", false, err
	}
	// The engine failed inside the page: this page has no text, and the
	// next one gets a fresh module.
	if cerr := d.ctx.Err(); cerr != nil {
		return "", false, cerr
	}
	d.broken = true
	return "", false, errPDFPage
}

// pageText reads the text of page i: errPDFPage when PDFium cannot load
// the page, another error when a call failed.
func (d *pdfiumDoc) pageText(i, maxBytes int) (string, bool, error) {
	page, err := d.inst.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: d.doc, Index: i})
	if errors.Is(err, pdfiumerrors.ErrPage) {
		return "", false, errPDFPage
	}
	if err != nil {
		return "", false, d.fail(err)
	}
	// A broken module is not called again, not even to close what it
	// holds: the next page gets a fresh one.
	defer func() {
		if !d.broken {
			_, _ = d.inst.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: page.Page})
		}
	}()
	ref := requests.Page{ByReference: &page.Page}
	if err := d.scanHidden(ref); err != nil {
		return "", false, err
	}
	tp, err := d.inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: ref})
	if err != nil {
		// PDFium makes a text page for every page it loaded, so this is
		// the engine failing.
		return "", false, d.fail(err)
	}
	defer func() {
		if !d.broken {
			_, _ = d.inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: tp.TextPage})
		}
	}()
	cc, err := d.inst.FPDFText_CountChars(&requests.FPDFText_CountChars{TextPage: tp.TextPage})
	if err != nil {
		return "", false, d.fail(err)
	}
	// CRLF becomes LF below, so the text read may be up to twice what is
	// kept: reading stops once it is certainly more.
	var b strings.Builder
	start := 0
	for start < cc.Count && b.Len() <= 2*maxBytes+1 {
		// go-pdfium decodes each chunk as UTF-16 with a byte order mark
		// honoured, so a chunk must not start with a character whose
		// encoding looks like one: U+FEFF is dropped (as the bridge's
		// clean() would drop it) and U+FFFE taken as below.
		u, err := d.inst.FPDFText_GetUnicode(&requests.FPDFText_GetUnicode{TextPage: tp.TextPage, Index: start})
		if err != nil {
			return "", false, d.fail(err)
		}
		switch u.Unicode {
		case 0xFEFF:
			start++
			continue
		case pdfiumLineHyphen:
			b.WriteByte('-')
			start++
			continue
		}
		n := min(pdfTextChunk, cc.Count-start)
		t, err := d.inst.FPDFText_GetText(&requests.FPDFText_GetText{TextPage: tp.TextPage, StartIndex: start, Count: n})
		if err != nil {
			return "", false, d.fail(err)
		}
		b.WriteString(t.Text)
		start += n
	}
	// PDFium ends the lines it makes with CRLF.
	text := strings.ReplaceAll(b.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, string(rune(pdfiumLineHyphen)), "-")
	if start < cc.Count || len(text) > maxBytes {
		return runePrefix(text, maxBytes), true, nil
	}
	return text, false, nil
}

// scanHidden looks for a text object drawn invisibly (render mode 3 or 7:
// the text layer of an OCR'd scan, or text hidden on purpose) until one is
// found or the budget is spent. Only the page's own objects are looked at,
// not those inside form XObjects.
func (d *pdfiumDoc) scanHidden(page requests.Page) error {
	if d.hidden || d.objects >= pdfObjectsPerDocument {
		return nil
	}
	count, err := d.inst.FPDFPage_CountObjects(&requests.FPDFPage_CountObjects{Page: page})
	if err != nil {
		return d.fail(err)
	}
	n := min(count.Count, pdfObjectsPerPage, pdfObjectsPerDocument-d.objects)
	for k := 0; k < n; k++ {
		d.objects++
		obj, err := d.inst.FPDFPage_GetObject(&requests.FPDFPage_GetObject{Page: page, Index: k})
		if err != nil && err.Error() == pdfiumNoObject {
			continue
		}
		if err != nil {
			return d.fail(err)
		}
		typ, err := d.inst.FPDFPageObj_GetType(&requests.FPDFPageObj_GetType{PageObject: obj.PageObject})
		if err != nil {
			return d.fail(err)
		}
		if typ.Type != enums.FPDF_PAGEOBJ_TEXT {
			continue
		}
		mode, err := d.inst.FPDFTextObj_GetTextRenderMode(&requests.FPDFTextObj_GetTextRenderMode{PageObject: obj.PageObject})
		if err != nil {
			return d.fail(err)
		}
		if mode.TextRenderMode == enums.FPDF_TEXTRENDERMODE_INVISIBLE || mode.TextRenderMode == enums.FPDF_TEXTRENDERMODE_CLIP {
			d.hidden = true
			return nil
		}
	}
	return nil
}

func (d *pdfiumDoc) HiddenText() bool { return d.hidden }

// shut closes the module and the runtime. Their errors say nothing new:
// a module stopped at a deadline fails every call. A broken module is not
// asked to close its document: it is dropped as it is.
func (d *pdfiumDoc) shut() {
	if d.inst != nil {
		if d.broken || d.ctx.Err() != nil {
			_ = d.inst.Kill()
		} else {
			_ = d.inst.Close()
		}
		d.inst = nil
	}
	if d.pool != nil {
		_ = d.pool.Close()
		d.pool = nil
	}
	d.doc = ""
}

func (d *pdfiumDoc) Close() {
	d.shut()
	d.cancel()
}

// reservedMemory backs a wasm linear memory. wazero's own allocator makes
// a new buffer of exactly the new size at every growth and copies the
// memory into it: a module growing towards its ceiling in small steps
// copies again and again and briefly holds its memory twice (the spike
// measured up to 1.65 GiB resident for a 512 MiB ceiling). This one
// doubles the buffer up to pdfMemoryEager, so that an ordinary document
// costs a few small copies, and past that allocates the whole ceiling
// once, so that a memory bomb costs one more copy of at most
// pdfMemoryEager. A buffer made once at the ceiling from the start would
// copy nothing but count its full size in the Go heap from the first
// document on, and Go zeroes, and so makes resident, the pages of such an
// allocation whenever they are ones the compile used before. Plain Go, no
// mmap.
type reservedMemory struct {
	max uint64
	buf []byte
}

func newReservedMemory(_, maxBytes uint64) experimental.LinearMemory {
	return &reservedMemory{max: maxBytes}
}

func (m *reservedMemory) Reallocate(size uint64) []byte {
	if size > m.max {
		return nil
	}
	if size > uint64(cap(m.buf)) {
		c := m.max
		if size <= pdfMemoryEager {
			c = min(max(size, 2*uint64(cap(m.buf))), pdfMemoryEager)
		}
		buf := make([]byte, len(m.buf), c)
		copy(buf, m.buf)
		m.buf = buf
	}
	m.buf = m.buf[:size]
	return m.buf
}

func (m *reservedMemory) Free() { m.buf = nil }
