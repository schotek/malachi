// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"io"
	"net/url"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// syncFolder brings one folder up to date with a delta query and then
// downloads the bodies it lacks. It returns the remote ids the server
// reports gone from the folder; the caller deletes them after every folder
// of the pass ran, so a message that reappears in another folder is moved
// (MoveByRemoteID) rather than deleted and re-created.
//
// Without a stored delta cursor (first pass, a full resync, or a cursor the
// service rejected) the query enumerates the whole window; rows the server
// did not mention are then gone as well.
func (s *Syncer) syncFolder(ctx context.Context, f store.Folder, since time.Time, full bool, progress func(float64)) ([]string, error) {
	initial := full || f.DeltaLink == ""
	link := f.DeltaLink
	if initial {
		link = s.initialDeltaURL(f, since)
	}
	knownList, err := s.deps.Store.ListRemoteIDs(ctx, f.ID)
	if err != nil {
		return nil, storageError(err)
	}
	known := make(map[string]bool, len(knownList))
	for _, id := range knownList {
		known[id] = true
	}

	seen := map[string]bool{}
	created := map[string]bool{}
	var tombstones []string
	var deltaLink string
	pages := 0
	for next := link; next != ""; {
		var pg page[message]
		err := s.client.Get(ctx, next, &pg)
		if err != nil && !initial && isSyncStateError(err) {
			// The cursor is stale: start over for this folder.
			s.log.Info("delta cursor rejected, resynchronising folder", "folder", f.ID)
			initial, pages = true, 0
			seen, created, tombstones = map[string]bool{}, map[string]bool{}, nil
			next = s.initialDeltaURL(f, since)
			continue
		}
		if err != nil {
			return nil, err
		}
		pages++
		var batch []*store.Message
		for _, m := range pg.Value {
			if m.ID == "" {
				continue
			}
			if m.Removed != nil {
				tombstones = append(tombstones, m.ID)
				delete(seen, m.ID)
				continue
			}
			seen[m.ID] = true
			exists := known[m.ID]
			if !exists {
				moved, err := s.deps.Store.MoveByRemoteID(ctx, s.account.ID, m.ID, f.ID)
				if err != nil {
					return nil, storageError(err)
				}
				exists = moved
			}
			if exists {
				if _, err := s.deps.Store.ApplyServerFlagsByRemoteID(ctx, f.ID, m.ID, flagsOf(m), 0); err != nil && !errors.Is(err, store.ErrNotFound) {
					return nil, storageError(err)
				}
				known[m.ID] = true
				continue
			}
			batch = append(batch, s.messageRow(f, m))
			known[m.ID] = true
			created[m.ID] = true
		}
		if len(batch) > 0 {
			if err := s.deps.Store.UpsertMessages(ctx, batch); err != nil {
				return nil, storageError(err)
			}
		}
		if pg.DeltaLink != "" {
			deltaLink = pg.DeltaLink
		}
		next = pg.NextLink
		progress(0.3 * min(1, float64(pages)/float64(pages+1)))
	}
	if initial {
		// Everything the enumeration did not mention is gone or outside
		// the window.
		for id := range known {
			if !seen[id] {
				tombstones = append(tombstones, id)
			}
		}
	} else if !since.IsZero() {
		old, err := s.deps.Store.ListRemoteIDsOlderThan(ctx, f.ID, since)
		if err != nil {
			return nil, storageError(err)
		}
		tombstones = append(tombstones, old...)
	}
	if err := s.deps.Store.SetFolderSyncState(ctx, f.ID, store.FolderSyncState{
		DeltaLink: deltaLink, ServerMessages: f.ServerMessages, ServerUnseen: f.ServerUnseen, LastSyncAt: s.now(),
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, storageError(err)
	}
	progress(0.3)
	// Bodies of messages created before this pass ran the first time are
	// not "new mail"; on an initial pass nothing is announced.
	announce := created
	if initial {
		announce = nil
	}
	if err := s.fetchBodies(ctx, f, announce, func(frac float64) { progress(0.3 + 0.7*frac) }); err != nil {
		return nil, err
	}
	progress(1)
	return tombstones, nil
}

// initialDeltaURL is the first request of a full enumeration, bounded by
// the retention window.
func (s *Syncer) initialDeltaURL(f store.Folder, since time.Time) string {
	q := url.Values{}
	q.Set("$select", messageSelect)
	if !since.IsZero() {
		q.Set("$filter", "receivedDateTime ge "+since.UTC().Format(time.RFC3339))
	}
	return "me/mailFolders/" + url.PathEscape(f.Mailbox) + "/messages/delta?" + q.Encode()
}

// messageRow is the store row of a listed message (headers only; the body
// comes later).
func (s *Syncer) messageRow(f store.Folder, m message) *store.Message {
	row := &store.Message{
		AccountID: s.account.ID, FolderID: f.ID, RemoteID: m.ID,
		Flags:          flagsOf(m),
		To:             addressesOf(m.ToRecipients),
		CC:             addressesOf(m.CCRecipients),
		ReplyTo:        addressesOf(m.ReplyTo),
		Subject:        mime.CleanHeaderText(m.Subject),
		Date:           m.SentDateTime,
		InternalDate:   m.ReceivedDateTime,
		RFCMessageID:   mime.TrimMessageID(m.InternetMessageID),
		HasAttachments: m.HasAttachments,
		ThreadID:       m.ConversationID,
		BodyState:      store.BodyNone,
	}
	if m.From != nil {
		row.From = addressesOf([]recipient{*m.From})
	}
	if row.Date.IsZero() {
		row.Date = m.ReceivedDateTime
	}
	return row
}

// fetchBodies downloads the bodies the folder still lacks, newest first,
// bodyConcurrency at a time. A message the server no longer has, or that
// cannot be parsed, is settled as failed so the loop cannot stall; one
// over the raw cap is tooBig.
func (s *Syncer) fetchBodies(ctx context.Context, f store.Folder, announce map[string]bool, progress func(float64)) error {
	attempted := map[string]bool{}
	done := 0
	for {
		refs, err := s.deps.Store.ListUnfetched(ctx, f.ID, unfetchedBatch)
		if err != nil {
			return storageError(err)
		}
		var todo []store.MessageRef
		for _, r := range refs {
			if r.RemoteID == "" || attempted[r.ID] {
				if attempted[r.ID] {
					s.log.Warn("body still unfetched after an attempt", "message", r.ID)
				}
				if err := s.settleBody(ctx, f, r, announce, store.BodyFailed); err != nil {
					return err
				}
				continue
			}
			attempted[r.ID] = true
			todo = append(todo, r)
		}
		if len(todo) == 0 {
			break
		}
		total := done + len(todo)
		if err := s.fetchBatch(ctx, f, todo, announce, func(n int) {
			done += n
			progress(float64(done) / float64(total))
		}); err != nil {
			return err
		}
	}
	progress(1)
	return nil
}

// fetchBatch downloads a batch concurrently; the first failure that is
// not about a single message ends the batch.
func (s *Syncer) fetchBatch(ctx context.Context, f store.Folder, batch []store.MessageRef, announce map[string]bool, advance func(int)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, bodyConcurrency)
	for _, ref := range batch {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			if firstErr != nil {
				return firstErr
			}
			return ctx.Err()
		}
		wg.Add(1)
		go func(ref store.MessageRef) {
			defer wg.Done()
			defer func() { <-sem }()
			err := s.fetchBody(ctx, f, ref, announce)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
				cancel()
			} else if err == nil {
				advance(1)
			}
			mu.Unlock()
		}(ref)
	}
	wg.Wait()
	return firstErr
}

// fetchBody streams one raw message into the store and parses it.
func (s *Syncer) fetchBody(ctx context.Context, f store.Folder, ref store.MessageRef, announce map[string]bool) error {
	rc, err := s.client.GetRaw(ctx, "me/messages/"+url.PathEscape(ref.RemoteID)+"/$value")
	switch {
	case IsNotFound(err):
		return s.settleBody(ctx, f, ref, announce, store.BodyFailed)
	case err != nil:
		return err
	}
	size, err := s.deps.Store.WriteMessageRaw(ctx, s.account.ID, ref.ID, rc, maxRawMessageBytes)
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 1<<20))
	rc.Close()
	switch {
	case errors.Is(err, store.ErrTooBig):
		return s.settleBody(ctx, f, ref, announce, store.BodyTooBig)
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return storageError(err)
	}
	raw, err := s.deps.Store.OpenMessageRaw(ctx, s.account.ID, ref.ID)
	if err != nil {
		return storageError(err)
	}
	parsed, perr := mime.Parse(raw, mime.DefaultLimits())
	raw.Close()
	if perr != nil {
		s.log.Warn("message body unparsable", "message", ref.ID, "err", perr)
		return s.settleBody(ctx, f, ref, announce, store.BodyFailed)
	}
	u := store.BodyUpdate{
		Text:           parsed.Text,
		HasHTML:        parsed.HasHTML,
		Snippet:        parsed.Snippet,
		Attachments:    parsed.Attachments,
		HasAttachments: parsed.HasAttachments,
		Headers:        parsed.Headers,
		References:     parsed.References,
		State:          store.BodyFetched,
		Subject:        parsed.Subject,
		From:           parsed.From,
		Date:           parsed.Date,
		RFCMessageID:   parsed.MessageID,
		InReplyTo:      parsed.InReplyTo,
		Size:           size,
	}
	if err := s.deps.Store.SetMessageBody(ctx, ref.ID, u); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // deleted meanwhile
		}
		return storageError(err)
	}
	s.notifyNew(ctx, f, ref, announce)
	return nil
}

// settleBody records a terminal body state and reports the message.
func (s *Syncer) settleBody(ctx context.Context, f store.Folder, ref store.MessageRef, announce map[string]bool, state store.BodyState) error {
	if err := s.deps.Store.MarkBodyState(ctx, ref.ID, state); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return storageError(err)
	}
	s.notifyNew(ctx, f, ref, announce)
	return nil
}

// notifyNew emits notify.newMessage for a message that arrived in this
// pass (docs/api.md §5). Folders holding the user's own mail (sent,
// drafts, trash, junk, outbox) never notify.
func (s *Syncer) notifyNew(ctx context.Context, f store.Folder, ref store.MessageRef, announce map[string]bool) {
	if s.deps.Notifier == nil || !announce[ref.RemoteID] {
		return
	}
	switch f.Role {
	case api.RoleSent, api.RoleDrafts, api.RoleTrash, api.RoleJunk, api.RoleOutbox:
		return
	}
	m, err := s.deps.Store.GetMessage(ctx, s.account.ID, ref.ID)
	if err != nil {
		return
	}
	s.deps.Notifier.NewMessage(api.NewMessageNotification{
		AccountID: api.AccountID(s.account.ID),
		FolderID:  api.FolderID(f.ID),
		Message:   summaryOf(m),
	})
}

// summaryOf is the list-view projection of a stored message.
func summaryOf(m store.Message) api.MessageSummary {
	sum := api.MessageSummary{
		ID:             api.MessageID(m.ID),
		AccountID:      api.AccountID(m.AccountID),
		FolderID:       api.FolderID(m.FolderID),
		ThreadID:       api.ThreadID(m.ThreadID),
		From:           m.From,
		To:             m.To,
		Subject:        m.Subject,
		Date:           m.Date,
		Snippet:        m.Snippet,
		Flags:          m.Flags,
		HasAttachments: m.HasAttachments,
		Size:           m.Size,
	}
	if sum.From == nil {
		sum.From = []api.Address{}
	}
	if sum.Flags == nil {
		sum.Flags = []api.Flag{}
	}
	if sum.Date.IsZero() {
		sum.Date = m.InternalDate
	}
	return sum
}
