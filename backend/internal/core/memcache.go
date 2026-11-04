// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"container/list"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The messages message.download keeps in memory under
// Preferences.NeverStoreAttachments (docs/api.md §4.3): the whole message
// as the server sent it, which the store holds only as a skeleton, for
// message.part, message.embedded, draft.create and draft.open to take the
// attachments from while it lasts. The cache is bounded by bytes, drops
// the least recently used message first and any message unused for
// memCacheIdle, and drops everything when the daemon quits
// (Backend.Close) or the preference is switched off, an account's
// messages when the account is removed or paused. Nothing of it is ever
// written to disk or logged.

// The bounds of the cache; variables so that tests can shrink them.
var (
	// memCacheCap bounds the bytes of all the messages held.
	memCacheCap int64 = 256 << 20
	// memCacheIdle is how long a message is held without being used.
	memCacheIdle = 30 * time.Minute
)

// heldMessage is one message the cache holds: the bytes as downloaded and
// the attachment list of their parse, which may number the parts
// differently from the stored skeleton (Microsoft 365 rebuilds the MIME).
// Neither is ever changed once held.
type heldMessage struct {
	raw         []byte
	attachments []api.Attachment
}

type memKey struct{ account, id string }

type memEntry struct {
	key  memKey
	msg  heldMessage
	used time.Time
}

// memCache is the cache; its zero value is ready to use and every method
// is safe for concurrent use.
type memCache struct {
	// now is the clock of the idle expiry; nil = time.Now. A test hook.
	now func() time.Time

	mu      sync.Mutex
	entries map[memKey]*list.Element // of *memEntry
	lru     list.List                // most recently used first
	bytes   int64
	// gen counts the clears: a download that started before one does not
	// put its message afterwards (put).
	gen    uint64
	timer  *time.Timer // the sweep of the idle entries, armed while any is held
	closed bool
}

func (c *memCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// generation is the count of clears so far, for a later put.
func (c *memCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// put holds a message, replacing what was held under the same key, and
// evicts the least recently used messages until the bytes fit. It holds
// nothing when the cache was cleared since gen was read (the preference
// switched off meanwhile), after close, and for a message over the whole
// cap. raw is kept as it is, not copied: the caller hands over bytes
// nothing else refers to or changes (a download received into memory).
func (c *memCache) put(gen uint64, accountID, id string, raw []byte, atts []api.Attachment) bool {
	size := int64(len(raw))
	if size > memCacheCap {
		return false
	}
	msg := heldMessage{raw: raw, attachments: slices.Clone(atts)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || gen != c.gen {
		return false
	}
	if c.entries == nil {
		c.entries = map[memKey]*list.Element{}
	}
	key := memKey{accountID, id}
	if el, ok := c.entries[key]; ok {
		c.removeLocked(el)
	}
	for c.bytes+size > memCacheCap && c.lru.Len() > 0 {
		c.removeLocked(c.lru.Back())
	}
	c.entries[key] = c.lru.PushFront(&memEntry{key: key, msg: msg, used: c.clock()})
	c.bytes += size
	c.armLocked()
	return true
}

// get returns the message held under the key and marks it used; false
// when none is held, or it has been idle too long (it goes then).
func (c *memCache) get(accountID, id string) (heldMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[memKey{accountID, id}]
	if !ok {
		return heldMessage{}, false
	}
	e := el.Value.(*memEntry)
	now := c.clock()
	if !now.Before(e.used.Add(memCacheIdle)) {
		c.removeLocked(el)
		return heldMessage{}, false
	}
	e.used = now
	c.lru.MoveToFront(el)
	return e.msg, true
}

// remove lets go of the message held under the key, if any.
func (c *memCache) remove(accountID, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[memKey{accountID, id}]; ok {
		c.removeLocked(el)
		c.armLocked()
	}
}

// dropAccount lets go of the messages of an account (removed or paused).
func (c *memCache) dropAccount(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, el := range c.entries {
		if key.account == accountID {
			c.removeLocked(el)
		}
	}
	c.armLocked()
}

// clear lets go of every message, and makes the puts of the downloads
// already running hold nothing (generation).
func (c *memCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearLocked()
}

// close clears the cache for good: nothing is held from then on.
func (c *memCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.clearLocked()
}

func (c *memCache) clearLocked() {
	c.gen++
	clear(c.entries)
	c.lru.Init()
	c.bytes = 0
	c.armLocked()
}

// sweep drops the messages idle for memCacheIdle; the timer runs it.
func (c *memCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	for el := c.lru.Back(); el != nil; el = c.lru.Back() {
		if now.Before(el.Value.(*memEntry).used.Add(memCacheIdle)) {
			break
		}
		c.removeLocked(el)
	}
	c.armLocked()
}

// stats reports how many messages the cache holds and their bytes.
func (c *memCache) stats() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len(), c.bytes
}

func (c *memCache) removeLocked(el *list.Element) {
	e := c.lru.Remove(el).(*memEntry)
	delete(c.entries, e.key)
	c.bytes -= int64(len(e.msg.raw))
}

// armLocked schedules the sweep for when the least recently used message
// expires, or stops it when nothing is held. A get that uses that message
// meanwhile only makes the sweep find nothing to drop yet.
func (c *memCache) armLocked() {
	back := c.lru.Back()
	if back == nil || c.closed {
		if c.timer != nil {
			c.timer.Stop()
		}
		return
	}
	d := max(back.Value.(*memEntry).used.Add(memCacheIdle).Sub(c.clock()), 0)
	if c.timer == nil {
		c.timer = time.AfterFunc(d, c.sweep)
		return
	}
	c.timer.Reset(d)
}

// heldPart takes a part of a stored message, as the message's row
// describes it (want, one of m.Attachments), from the whole copy
// message.download holds in memory, decoded and capped at maxBytes. held
// is false when no copy is held or the copy has no part that is want
// (heldPartID); err is mime.ExtractPart's other failures. The part keeps
// want's id. Using the copy keeps it held.
func (b *Backend) heldPart(m store.Message, want api.Attachment, maxBytes int64) (part *mime.Part, held bool, err error) {
	msg, ok := b.mem.get(m.AccountID, m.ID)
	if !ok {
		return nil, false, nil
	}
	id := heldPartID(want, msg.attachments)
	if id == "" {
		return nil, false, nil
	}
	part, err = mime.ExtractPart(bytes.NewReader(msg.raw), id, mime.DefaultLimits(), maxBytes)
	switch {
	case errors.Is(err, mime.ErrPartNotFound):
		return nil, false, nil
	case err != nil:
		return nil, true, err
	}
	part.PartID = want.PartID
	return part, true, nil
}

// heldPartID finds the part of a held copy that is the attachment want of
// the stored message, whose server may have numbered the parts anew
// (Microsoft 365 rebuilds the MIME): the part with want's id, name, type
// and size; else the only one with its name, type and size; else the only
// one with its name and type. A part whose size differs from want's is
// never taken while another has it. "" when there is none, or no single
// one.
func heldPartID(want api.Attachment, held []api.Attachment) string {
	var named, sized []string
	for _, a := range held {
		if a.Filename != want.Filename || a.ContentType != want.ContentType {
			continue
		}
		named = append(named, a.PartID)
		if a.Size != want.Size {
			continue
		}
		if a.PartID == want.PartID {
			return a.PartID
		}
		sized = append(sized, a.PartID)
	}
	switch {
	case len(sized) == 1:
		return sized[0]
	case len(sized) == 0 && len(named) == 1:
		return named[0]
	}
	return ""
}

// heldMissing lists the parts the row of a stored message keeps on the
// server (m.RemoteParts) that a whole copy of it, whose parse lists held,
// has none of (heldPartID): a copy that lacks any cannot stand in for the
// stored message. Empty when the copy has every one.
func heldMissing(m store.Message, held []api.Attachment) []string {
	var missing []string
	for _, id := range m.RemoteParts {
		want, ok := rowAttachment(m, id)
		if !ok || heldPartID(want, held) == "" {
			missing = append(missing, id)
		}
	}
	return missing
}

// rowAttachment is the attachment partID of a message's row; false when
// the row lists none.
func rowAttachment(m store.Message, partID string) (api.Attachment, bool) {
	for _, a := range m.Attachments {
		if a.PartID == partID {
			return a, true
		}
	}
	return api.Attachment{}, false
}
