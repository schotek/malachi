// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"sync"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The own text of the user's messages that have HTML (docs/api.md §4.13
// "own text"): the rules search the user's own words for a question mark
// (them.asked) and a commitment's quote must be among them. The plain-text
// part of such a message is often the editor's innerText, whose quoted
// original carries no ">" marks; its HTML still has the quote's structure.
// So the own text is the text of the HTML with the quoted history cut off
// by the same trimming message.body uses with trimQuoted (sanitize's
// TrimQuoted in the view mode, under the block policy), rendered as plain
// text by the sanitiser; the rules then cut what is left (board.OwnText).
//
// Deriving it means parsing the raw message and sanitising its HTML, which
// must not happen inside the drain's write transaction. So the decider
// looks the own text up in a cache and, for a member it has none for,
// leaves the thread as it is (Skip) and asks for it (boardWants); the
// worker derives the own texts after the batch, outside any transaction,
// and marks those threads dirty again. Only the members the rules read
// text of ask for it (board.Verdict.TextMembers: the user's newest
// messages, a bounded few), never every member of a long thread. A thread asked
// for once is judged with what there is the next time (boardOwnCache's
// retried), so that an evicted entry can never stall it.

// boardOwnTextCap caps an own text kept in the cache; the rules look at
// the first 8 KiB of the own text for a question mark.
const boardOwnTextCap = 64 << 10

// boardOwnCacheBytes bounds one generation of the cache (two are kept).
const boardOwnCacheBytes = 8 << 20

// boardOwnKey names one member's own text: the body state is part of it,
// so that a body fetched later is read again.
type boardOwnKey struct {
	account, id string
	state       store.BodyState
}

// boardOwnText is a cached own text; html false: the message has no HTML
// (the rules use its plain text). trimmed: the sanitiser cut a quoted
// history off the HTML (board.get uses the text only then).
type boardOwnText struct {
	text    string
	html    bool
	trimmed bool
}

// boardOwnCache holds the own texts in two generations: lookups see both,
// a new entry goes into the current one, which becomes the old one when it
// is full (the old one is dropped then). retried are the threads judged
// once without some own text ("account\x00thread").
type boardOwnCache struct {
	mu       sync.Mutex
	cur, old map[boardOwnKey]boardOwnText
	bytes    int
	retried  map[string]bool
}

func (c *boardOwnCache) init() {
	c.cur, c.old, c.retried = map[boardOwnKey]boardOwnText{}, map[boardOwnKey]boardOwnText{}, map[string]bool{}
}

func (c *boardOwnCache) get(k boardOwnKey) (boardOwnText, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.cur[k]; ok {
		return v, true
	}
	v, ok := c.old[k]
	return v, ok
}

func (c *boardOwnCache) put(k boardOwnKey, v boardOwnText) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bytes+len(v.text) > boardOwnCacheBytes || len(c.cur) >= 4096 {
		c.old, c.cur, c.bytes = c.cur, map[boardOwnKey]boardOwnText{}, 0
	}
	c.cur[k] = v
	c.bytes += len(v.text) + len(k.account) + len(k.id)
}

// retry records that a thread waits for own texts; takeRetry reports
// whether it did, and forgets it.
func (c *boardOwnCache) retry(thread string) {
	c.mu.Lock()
	c.retried[thread] = true
	c.mu.Unlock()
}

func (c *boardOwnCache) takeRetry(thread string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.retried[thread]
	delete(c.retried, thread)
	return ok
}

// boardWants are the own texts one batch's decider asked for, and the
// threads waiting for them.
type boardWants struct {
	keys    []boardOwnKey
	threads map[string][]string // account → threads
}

func (w *boardWants) empty() bool { return len(w.threads) == 0 }

func (w *boardWants) add(accountID, threadID string, keys []boardOwnKey) {
	if w.threads == nil {
		w.threads = map[string][]string{}
	}
	w.keys = append(w.keys, keys...)
	w.threads[accountID] = append(w.threads[accountID], threadID)
}

// loadBoardOwnTexts derives the own texts w asks for into the cache and
// marks its threads dirty again; the next batch judges them with those.
func (b *Backend) loadBoardOwnTexts(ctx context.Context, w *boardWants) error {
	c := &b.board.own
	for _, k := range w.keys {
		if _, ok := c.get(k); ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.boardOwnTextCached(ctx, k)
	}
	for account, threads := range w.threads {
		for _, th := range threads {
			c.retry(account + "\x00" + th)
		}
		if err := b.store.MarkBoardThreadsDirty(ctx, account, threads); err != nil {
			return err
		}
	}
	return nil
}

// boardOwnText derives the own text of a message from its HTML part: the
// text of the sanitised HTML with the quoted history cut off, as
// message.body with trimQuoted shows it (html.go). html is false when the
// message has no HTML (or no fetched body): the caller then uses its plain
// text. A message whose HTML cannot be read or sanitised has no own text
// (fails closed: "", true).
func (b *Backend) boardOwnText(ctx context.Context, accountID, id string) (text string, html bool) {
	own := b.deriveBoardOwnText(ctx, accountID, id)
	return own.text, own.html
}

// boardOwnTextCached is the own text of k from the cache, derived and
// cached when it is not there (the cap of the cache applied).
func (b *Backend) boardOwnTextCached(ctx context.Context, k boardOwnKey) boardOwnText {
	c := &b.board.own
	if own, ok := c.get(k); ok {
		return own
	}
	own := b.deriveBoardOwnText(ctx, k.account, k.id)
	own.text = store.CutUTF8(own.text, boardOwnTextCap)
	c.put(k, own)
	return own
}

// deriveBoardOwnText is boardOwnText with whether the HTML lost a quoted
// history.
func (b *Backend) deriveBoardOwnText(ctx context.Context, accountID, id string) boardOwnText {
	_, hasHTML, state, err := b.store.GetMessageText(ctx, accountID, id)
	if err != nil || !hasHTML || state != store.BodyFetched {
		return boardOwnText{}
	}
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			b.log.Warn("board: read a message", "message", id, "err", err)
		}
		return boardOwnText{html: true}
	}
	parsed, err := mime.Parse(f, mime.DefaultLimits())
	f.Close()
	if err != nil || !parsed.HasHTML {
		return boardOwnText{html: true}
	}
	out, err := b.Sanitize(sanitize.Input{
		HTML:          parsed.RawHTML,
		Mode:          sanitize.ModeView,
		Policy:        api.RemoteBlock,
		MaxOutputSize: viewHTMLCap,
		TrimQuoted:    true,
	})
	if err != nil {
		return boardOwnText{html: true}
	}
	return boardOwnText{text: out.Text, html: true, trimmed: out.QuotedTrimmed}
}

// boardExcerptSource is the text board.get makes a message's excerpt of
// (board.BoardMessageExcerpt) and whether a quoted history was cut off it
// already: the quoted history goes as message.body with trimQuoted takes
// it off (html.go sanitizeInto). The stored plain text, when the text
// rules find the quote in it (the excerpt cuts it there), or when the
// message has no HTML or no fetched body; else, when the sanitiser cut a
// quoted history off the HTML part, the text of the trimmed HTML (an
// Outlook text alternative or Gmail's innerText, whose quote the text
// rules cannot see); else the stored text whole. The HTML's own text
// comes from the cache the rules use (the same derivation), so opening a
// case parses only the members neither has seen. In doubt — the HTML
// unreadable, refused, or its trimming given up — the stored text.
func (b *Backend) boardExcerptSource(ctx context.Context, accountID string, m store.BoardMessage) (string, bool) {
	if m.BodyState != store.BodyFetched {
		return m.Text, false
	}
	if _, ok := sanitize.TrimQuotedText(m.Text); ok {
		return m.Text, false
	}
	own := b.boardOwnTextCached(ctx, boardOwnKey{account: accountID, id: m.ID, state: store.BodyFetched})
	if !own.html || !own.trimmed {
		return m.Text, false
	}
	return own.text, true
}
