// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The text of a document is extracted once and paged by offset over
// several get_attachment calls. Extraction is deterministic and so is
// clean(), so an offset stays valid without a cache; the cache only saves
// the work of a page: fetching up to 16 MiB through message.part again,
// a download from the mail server counted against the session's budget,
// and up to workerTimeout of a worker. A hit skips all of it.
//
// It is memory only: never written anywhere, never logged, gone when the
// process exits. It holds at most maxDocCacheEntries documents and
// maxDocCacheBytes, the least recently used going first, and drops an
// entry docCacheIdle after it was last used.
//
// Cached are the outcomes that come again for the same bytes: a text,
// every refusal (the worker's and the bridge's own byte checks), a crashed
// or broken worker and the memory watchdog. Never cached are the outcomes
// of the moment: a timeout, a worker that could not be started, a PDF
// engine that could not be started in it, all workers busy, a cancelled
// call, a worker of another protocol.

// Calls for one document share one reading of it. go-sdk runs tool calls
// at once and without a bound, and a call reading a document holds its
// bytes (up to 16 MiB) from the fetch until its worker is done; a worker's
// cold start peaks at a few hundred MiB. So:
//
//   - a call for a document (a docKey) that another call is reading waits
//     for that reading and answers with its outcome, whatever it is (a
//     text, a reason, the moment's too: busy, a timeout), holding nothing
//     meanwhile; only when the reading ended without an outcome (its call
//     was cancelled, or fetching the document failed, maybe because the
//     server rebuilt the message) does a waiting call start over, from
//     message.get, since the listing it has may be gone;
//   - at most maxDocumentReads calls read documents at once (fetching
//     one, waiting for a worker, or with one): maxConcurrentWorkers with a
//     worker and maxQueuedDocuments more. A call for another document
//     beyond them is answered busy at once, before it fetches anything,
//     and that answer is not cached.
//
// A waiting call ends with its own context; the reading goes on for the
// others.

// docKey names a document by what the current call's message.get says of
// it. Microsoft 365 renumbers parts when it serves a message again; a part
// whose name, type or size changed with it is another key.
type docKey struct {
	acc         api.AccountID
	msg         api.MessageID
	part        string
	filename    string
	contentType string
	size        int64
}

// docEntry is the cached outcome of one document: its cleaned text and
// facts, or the reason it is withheld.
type docEntry struct {
	format extract.Format
	text   string
	facts  extract.Facts
	reason string // non-empty: withheld, no text
}

// docEntryOverhead is what an entry counts beyond its text and reason.
const docEntryOverhead = 512

// cost is what an entry counts against maxDocCacheBytes.
func (e docEntry) cost(k docKey) int {
	return len(e.text) + len(e.reason) + len(k.acc) + len(k.msg) + len(k.part) +
		len(k.filename) + len(k.contentType) + docEntryOverhead
}

type docCacheItem struct {
	key   docKey
	entry docEntry
	cost  int
	used  time.Time
}

// docCache is the LRU of extracted documents, and the readings of
// documents under way. Safe for concurrent use.
type docCache struct {
	mu    sync.Mutex
	now   func() time.Time // time.Now; a test sets its own clock
	items map[docKey]*list.Element
	order *list.List // front: most recently used
	bytes int

	reads   map[docKey]*docReading // the readings under way, by the key they were begun for
	reading int                    // readings under way: at most maxDocumentReads
}

func newDocCache() *docCache {
	return &docCache{now: time.Now, items: map[docKey]*list.Element{}, order: list.New(), reads: map[docKey]*docReading{}}
}

// docReading is one call's reading of a document, which the calls for the
// same key that come meanwhile wait for.
type docReading struct {
	done    chan struct{} // closed when the reading ended
	entry   *docEntry     // set before done is closed; nil: it ended without an outcome
	waiters int           // calls that waited for it (docCache.mu), for tests
}

// docTurn is what a call for a document does (docCache.turn).
type docTurn int

const (
	turnCached docTurn = iota // answer from the cache
	turnWait                  // wait for another call's reading
	turnRead                  // read it: this call's reading, which it ends with docCache.end
	turnBusy                  // maxDocumentReads readings are under way: answer busy
)

// turn says what a call for document k does: answer with its cached entry,
// wait for the reading of it under way, read it itself (and end the
// reading with end), or answer busy.
func (c *docCache) turn(k docKey) (docTurn, docEntry, *docReading) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.getLocked(k); ok {
		return turnCached, e, nil
	}
	if r := c.reads[k]; r != nil {
		r.waiters++
		return turnWait, docEntry{}, r
	}
	if c.reading >= maxDocumentReads {
		return turnBusy, docEntry{}, nil
	}
	c.reading++
	r := &docReading{done: make(chan struct{})}
	c.reads[k] = r
	return turnRead, docEntry{}, r
}

// end ends reading r, begun for k: the calls waiting for it answer with e,
// or read the document themselves when e is nil. Called exactly once per
// reading, after its outcome was put into the cache when it is kept.
func (c *docCache) end(k docKey, r *docReading, e *docEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reads[k] == r {
		delete(c.reads, k)
	}
	c.reading--
	r.entry = e
	close(r.done)
}

// awaitDocument is the entry a call for document k (in format f) answers
// with: from the cache, from another call's reading of it, or busy. Or,
// with r, none: the call reads the document itself and ends r with
// b.docs.end. Or, with again, none either: the reading the call waited
// for ended without an outcome, and the call starts over. An error is
// ctx's, which ended while the call waited.
func (b *bridge) awaitDocument(ctx context.Context, k docKey, f extract.Format) (e docEntry, r *docReading, again bool, err error) {
	turn, e, r := b.docs.turn(k)
	switch turn {
	case turnCached:
		return e, nil, false, nil
	case turnBusy:
		b.log.Warn("document readers busy", "format", string(f))
		return docEntry{format: f, reason: workerFailureReason(extraction{busy: true})}, nil, false, nil
	case turnRead:
		return docEntry{}, r, false, nil
	}
	select {
	case <-r.done:
		if r.entry != nil {
			return *r.entry, nil, false, nil
		}
		return docEntry{}, nil, true, nil
	case <-ctx.Done():
		return docEntry{}, nil, false, ctx.Err()
	}
}

// waiting is how many calls waited for the reading of k under way, for
// tests; -1 when none is.
func (c *docCache) waiting(k docKey) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.reads[k]; r != nil {
		return r.waiters
	}
	return -1
}

// get returns the entry of k, marking it used; false for none or one idle
// for docCacheIdle.
func (c *docCache) get(k docKey) (docEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(k)
}

// getLocked is get; c.mu is held.
func (c *docCache) getLocked(k docKey) (docEntry, bool) {
	now := c.now()
	c.expire(now)
	el, ok := c.items[k]
	if !ok {
		return docEntry{}, false
	}
	it := el.Value.(*docCacheItem)
	it.used = now
	c.order.MoveToFront(el)
	return it.entry, true
}

// put stores the entry of k, replacing one already there, and evicts the
// least recently used entries until the cache is within its limits.
func (c *docCache) put(k docKey, e docEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expire(now)
	if el, ok := c.items[k]; ok {
		c.remove(el)
	}
	it := &docCacheItem{key: k, entry: e, cost: e.cost(k), used: now}
	if it.cost > maxDocCacheBytes {
		return
	}
	c.items[k] = c.order.PushFront(it)
	c.bytes += it.cost
	for c.order.Len() > maxDocCacheEntries || c.bytes > maxDocCacheBytes {
		c.remove(c.order.Back())
	}
}

// len is the number of entries, for tests.
func (c *docCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// expire drops the entries idle for docCacheIdle; c.mu is held.
func (c *docCache) expire(now time.Time) {
	for el := c.order.Back(); el != nil; {
		prev := el.Prev()
		if now.Sub(el.Value.(*docCacheItem).used) >= docCacheIdle {
			c.remove(el)
		}
		el = prev
	}
}

// remove drops one entry; c.mu is held.
func (c *docCache) remove(el *list.Element) {
	it := el.Value.(*docCacheItem)
	c.order.Remove(el)
	delete(c.items, it.key)
	c.bytes -= it.cost
}
