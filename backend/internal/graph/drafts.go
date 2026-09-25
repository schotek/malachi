// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// draftsPerPass bounds the uploads of one pass.
	draftsPerPass = 50
	// editSlack absorbs the clock skew between the service's
	// lastModifiedDateTime and the local time a copy was recorded at.
	editSlack = 2 * time.Minute
)

// pushDrafts stores the account's due drafts (store.DueDraftUploads) in
// the Drafts folder: POST me/messages with the base64 MIME that
// Deps.BuildDraft writes, which the service files as a draft there. The
// copy it replaces is deleted through an ordinary delete operation
// (MarkDraftSynced queues it; the operations are pushed again before
// returning) — unless the service says someone changed that copy after
// it was stored (Outlook edits drafts in place): then both stay rather
// than the other client's text being lost. A refusal, or a draft that
// cannot be built, is recorded on the draft with backoff; a transient
// failure or a storage error ends the pass. It returns the folders that
// received a copy.
func (s *Syncer) pushDrafts(ctx context.Context, byMailbox map[string]store.Folder) (map[string]bool, error) {
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

	touched := map[string]bool{}
	for _, d := range due {
		up, err := s.deps.BuildDraft(ctx, d.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue
		case isStorageFailure(err):
			return touched, storageError(err)
		case err != nil:
			if err := s.draftFailed(ctx, d, err); err != nil {
				return touched, err
			}
			continue
		}
		keep := false
		if d.Copy.RemoteID != "" && !d.SyncedAt.IsZero() {
			if keep, err = s.editedSince(ctx, d.Copy.RemoteID, d.SyncedAt); err != nil {
				return touched, err
			}
			if keep {
				s.log.Info("draft copy changed on the server, keeping it", "draft", d.ID)
			}
		}
		id, err := s.createDraft(ctx, up.Raw)
		if err != nil {
			if !isFinalRefusal(err) {
				return touched, err
			}
			if err := s.draftFailed(ctx, d, err); err != nil {
				return touched, err
			}
			continue
		}
		touched[folder.ID] = true
		c := store.DraftCopy{FolderID: folder.ID, RemoteID: id, RFCMessageID: up.RFCMessageID}
		found, err := s.deps.Store.MarkDraftSynced(ctx, s.account.ID, d.ID, up.Version, c, keep)
		if err != nil {
			return touched, storageError(err)
		}
		if found {
			s.log.Info("draft stored", "draft", d.ID, "version", up.Version)
		} else {
			s.log.Info("draft gone during its upload, removing the copy", "draft", d.ID)
		}
	}
	if len(touched) > 0 {
		more, err := s.pushOps(ctx, byMailbox)
		for id := range more {
			touched[id] = true
		}
		if err != nil {
			return touched, err
		}
	}
	return touched, nil
}

// createDraft uploads one message as a draft of the Drafts folder and
// returns its immutable id.
func (s *Syncer) createDraft(ctx context.Context, raw []byte) (string, error) {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(encoded, raw)
	body := func() (io.Reader, error) { return bytes.NewReader(encoded), nil }
	var out struct {
		ID string `json:"id"`
	}
	if err := s.client.PostRawInto(ctx, "me/messages", body, "text/plain", int64(len(encoded)), &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", &StatusError{Status: http.StatusBadGateway, Message: "created draft without an id"}
	}
	return out.ID, nil
}

// editedSince reports whether the service changed the item after t (plus
// editSlack). A copy that is gone, or that the service will not show, was
// not edited; a transient failure is returned.
func (s *Syncer) editedSince(ctx context.Context, remoteID string, t time.Time) (bool, error) {
	var out struct {
		LastModified time.Time `json:"lastModifiedDateTime"`
	}
	err := s.client.Get(ctx, "me/messages/"+url.PathEscape(remoteID)+"?$select=lastModifiedDateTime", &out)
	switch {
	case err == nil:
		return out.LastModified.After(t.Add(editSlack)), nil
	case IsNotFound(err), isFinalRefusal(err):
		return false, nil
	}
	return false, err
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
