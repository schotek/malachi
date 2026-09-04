// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"io"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// maxAppendAttempts bounds the outbox attempts (delivery included) after
// which a delivered message whose Sent copy the server keeps refusing is
// dropped locally: the mail was sent, only the copy is lost.
const maxAppendAttempts = 8

// appendSent uploads every delivered outbox message of the account (state
// sent, due now) to the folder with role sent: APPEND with \Seen and the
// message's date as INTERNALDATE, the raw file streamed as the literal. A
// stored copy is then deleted locally — the Sent folder's next pass fetches
// it back under its server UID. Without a sent folder, or when the local
// row or file is gone, the copy is dropped at once. A NO/BAD is recorded on
// the entry with backoff (dropped after maxAppendAttempts); any other error
// (dead connection, storage) ends the pass with the row untouched. It
// returns the mailboxes that received an APPEND.
func (s *Syncer) appendSent(ctx context.Context, sess *session) (map[string]bool, error) {
	now := s.now()
	entries, err := s.deps.Store.ListOutbox(ctx, s.account.ID, store.OutboxSent, now)
	if err != nil {
		return nil, storageError(err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	sent, err := s.deps.Store.FolderByRole(ctx, s.account.ID, api.RoleSent)
	haveSent := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, storageError(err)
	}
	appended := map[string]bool{}
	for _, e := range entries {
		if !haveSent {
			s.log.Info("no sent folder, dropping local copy of sent message", "message", e.MessageID)
			if err := s.dropSent(ctx, e.MessageID); err != nil {
				return appended, err
			}
			continue
		}
		ok, err := s.appendOne(ctx, sess, sent.Mailbox, e)
		if err != nil {
			return appended, err
		}
		if ok {
			appended[sent.Mailbox] = true
		}
	}
	return appended, nil
}

// appendOne uploads one entry; ok reports that the server accepted it.
func (s *Syncer) appendOne(ctx context.Context, sess *session, mailbox string, e store.OutboxEntry) (ok bool, err error) {
	m, err := s.deps.Store.GetMessage(ctx, s.account.ID, e.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn("sent message row missing, dropping outbox entry", "message", e.MessageID)
		return false, s.dropSent(ctx, e.MessageID)
	}
	if err != nil {
		return false, storageError(err)
	}
	raw, err := s.deps.Store.OpenMessageRaw(ctx, s.account.ID, e.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn("sent message file missing, dropping local copy", "message", e.MessageID)
		return false, s.dropSent(ctx, e.MessageID)
	}
	if err != nil {
		return false, storageError(err)
	}
	defer raw.Close()
	info, err := raw.Stat()
	if err != nil {
		return false, storageError(err)
	}
	size := info.Size()
	if size <= 0 {
		s.log.Warn("sent message file empty, dropping local copy", "message", e.MessageID)
		return false, s.dropSent(ctx, e.MessageID)
	}
	at := m.Date
	if at.IsZero() {
		at = m.InternalDate
	}
	if at.IsZero() {
		at = s.now()
	}

	err = sess.do(ctx, bodyBatchTimeout, func() error {
		cmd := sess.Append(mailbox, size, &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}, Time: at})
		_, copyErr := io.CopyN(cmd, raw, size)
		closeErr := cmd.Close()
		if copyErr != nil {
			// A short literal leaves the connection out of step with the
			// server; give it up so the next pass starts on a fresh one.
			sess.raw.Close()
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, err := cmd.Wait()
		return err
	})
	switch {
	case err == nil:
		s.log.Info("sent copy stored", "message", e.MessageID, "mailbox", mailbox)
		return true, s.dropSent(ctx, e.MessageID)
	case isStatusError(err):
		text := transport.CleanMessage(err.Error())
		if e.Attempts+1 >= maxAppendAttempts {
			s.log.Warn("sent copy refused repeatedly, dropping local copy", "message", e.MessageID, "attempts", e.Attempts+1, "err", text)
			return false, s.dropSent(ctx, e.MessageID)
		}
		s.log.Warn("sent copy refused", "message", e.MessageID, "attempts", e.Attempts+1, "err", text)
		retry := s.now().Add(opBackoff(e.Attempts))
		if err := s.deps.Store.MarkOutboxAppendFailed(ctx, e.MessageID, text, retry); err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, storageError(err)
		}
		return false, nil
	default:
		return false, err
	}
}

// dropSent removes the local copy of a delivered message together with its
// outbox entry; an entry that is already gone is fine.
func (s *Syncer) dropSent(ctx context.Context, id string) error {
	err := s.deps.Store.DeleteOutboxMessage(ctx, s.account.ID, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn("outbox message not found for removal", "message", id)
	}
	return nil
}
