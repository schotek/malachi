// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// draftsPerPass bounds the uploads of one pass; the rest waits for the next.
const draftsPerPass = 50

// pushDrafts stores the account's due drafts (store.DueDraftUploads: a
// version not on the server yet that has rested for Deps.DraftQuiet) in
// the folder with role drafts: APPEND with \Draft and \Seen of the message
// Deps.BuildDraft writes under a fresh Message-ID. The copy it replaces is
// deleted through an ordinary delete operation (MarkDraftSynced queues it;
// the operations are pushed again before returning), so every version
// ends up as exactly one message. Without a drafts folder the drafts stay
// local. A NO/BAD, or a draft that cannot be built, is recorded on that
// draft with backoff; a dead connection or a storage failure ends the
// pass. It returns the mailboxes that received an APPEND.
func (s *Syncer) pushDrafts(ctx context.Context, sess *session) (map[string]bool, error) {
	if s.deps.BuildDraft == nil {
		return nil, nil
	}
	due, err := s.deps.Store.DueDraftUploads(ctx, s.account.ID, s.now(), s.deps.DraftQuiet, draftsPerPass)
	if err != nil {
		return nil, storageError(err)
	}
	if len(due) == 0 {
		return nil, nil
	}
	folder, err := s.deps.Store.FolderByRole(ctx, s.account.ID, api.RoleDrafts)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Info("no drafts folder, drafts stay local", "count", len(due))
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}

	appended := map[string]bool{}
	for _, d := range due {
		up, err := s.deps.BuildDraft(ctx, d.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue // sent or deleted since the listing
		case isStorageFailure(err):
			return appended, storageError(err)
		case err != nil:
			if err := s.draftFailed(ctx, d, err); err != nil {
				return appended, err
			}
			continue
		}
		c, err := s.appendDraft(ctx, sess, folder, up)
		if err != nil {
			if !isStatusError(err) {
				return appended, err
			}
			if err := s.draftFailed(ctx, d, err); err != nil {
				return appended, err
			}
			continue
		}
		appended[folder.Mailbox] = true
		found, err := s.deps.Store.MarkDraftSynced(ctx, s.account.ID, d.ID, up.Version, c, false)
		if err != nil {
			return appended, storageError(err)
		}
		if found {
			s.log.Info("draft stored", "draft", d.ID, "version", up.Version, "mailbox", folder.Mailbox)
		} else {
			s.log.Info("draft gone during its upload, removing the copy", "draft", d.ID)
		}
	}
	if len(appended) > 0 {
		// The replaced copies are queued deletions now.
		if err := s.pushOps(ctx, sess); err != nil {
			return appended, err
		}
	}
	return appended, nil
}

// appendDraft uploads one built draft and returns where it went: the UID
// and UIDVALIDITY come from APPENDUID when the server offers UIDPLUS,
// otherwise the copy is known by its Message-ID until the folder's pass
// fetches it.
func (s *Syncer) appendDraft(ctx context.Context, sess *session, f store.Folder, up store.DraftUpload) (store.DraftCopy, error) {
	c := store.DraftCopy{FolderID: f.ID, RFCMessageID: up.RFCMessageID}
	size := int64(len(up.Raw))
	err := sess.do(ctx, bodyBatchTimeout, func() error {
		cmd := sess.Append(f.Mailbox, size, &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen},
			Time:  s.now(),
		})
		_, copyErr := io.Copy(cmd, bytes.NewReader(up.Raw))
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
		data, err := cmd.Wait()
		if err != nil {
			return err
		}
		if data != nil && data.UID != 0 && sess.caps.Has(imap.CapUIDPlus) {
			c.UID, c.UIDValidity = uint32(data.UID), data.UIDValidity
		}
		return nil
	})
	return c, err
}

// draftFailed records a failed upload on the draft with the operations'
// backoff; after store.MaxDraftSyncAttempts the draft waits for its next
// save.
func (s *Syncer) draftFailed(ctx context.Context, d store.Draft, cause error) error {
	text := transport.CleanMessage(cause.Error())
	if d.SyncAttempts+1 >= store.MaxDraftSyncAttempts {
		s.log.Warn("draft upload refused repeatedly, waiting for the next save", "draft", d.ID, "err", text)
	} else {
		s.log.Warn("draft upload failed", "draft", d.ID, "attempts", d.SyncAttempts+1, "err", text)
	}
	retry := s.now().Add(opBackoff(d.SyncAttempts))
	if err := s.deps.Store.MarkDraftSyncFailed(ctx, d.ID, text, retry); err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	return nil
}

// isStorageFailure reports the storage error of a BuildDraft (see Deps).
func isStorageFailure(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.CodeStorageError
}
