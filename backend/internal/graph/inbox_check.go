// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

var errNoCount = errors.New("the service sent no count")

const (
	// inboxCheckEvery is the least time between two count checks of the
	// inbox: the check costs a request, the poll runs every minute.
	inboxCheckEvery = 10 * time.Minute
	// inboxForceEvery is the least time between two enumerations the check
	// forces. Counts can differ for good reasons (an item that is not a
	// message, a local change in flight), and a mismatch that survives an
	// enumeration must not turn into one enumeration per check.
	inboxForceEvery = time.Hour
)

// checkInbox compares the server's message count of the inbox within the
// retention window with the local one after an incremental pass. Delta is
// authoritative on paper, but a message it never reported (observed with a
// real Microsoft 365 mailbox) would otherwise stay missing until
// reconcileAfter; on a mismatch the next pass of the inbox enumerates. It
// runs on the syncer goroutine after the tombstones were applied, and a
// check that cannot be made is skipped, never an error of the pass.
func (s *Syncer) checkInbox(ctx context.Context, f store.Folder, since time.Time) {
	now := s.now()
	if last, ok := s.checkedAt[f.ID]; ok && now.Sub(last) < inboxCheckEvery {
		return
	}
	s.checkedAt[f.ID] = now
	// Both sides count whole seconds: the filter cannot say more.
	since = since.UTC().Truncate(time.Second)

	server := f.ServerMessages
	if !since.IsZero() {
		n, err := s.serverCountSince(ctx, f, since)
		if err != nil {
			s.log.Info("inbox count check skipped", "folder", f.ID, "err", err)
			return
		}
		server = n
	}
	local, err := s.deps.Store.CountMessagesSince(ctx, f.ID, since)
	if err != nil {
		s.log.Info("inbox count check skipped", "folder", f.ID, "err", err)
		return
	}
	if server == local {
		delete(s.gaveUp, f.ID)
		return
	}
	if last, ok := s.forcedAt[f.ID]; ok && now.Sub(last) < inboxForceEvery {
		if !s.gaveUp[f.ID] {
			s.gaveUp[f.ID] = true
			s.log.Warn("inbox count still differs after an enumeration, not enumerating again for now",
				"folder", f.ID, "server", server, "local", local)
		}
		return
	}
	s.log.Info("inbox count differs from the server, enumerating the inbox",
		"folder", f.ID, "server", server, "local", local)
	s.forceEnumerate[f.ID] = true
	s.forcedAt[f.ID] = now
	delete(s.gaveUp, f.ID)
	s.Trigger(api.FolderID(f.ID), false)
}

// serverCountSince asks the service how many messages the folder holds
// from the cutoff on.
func (s *Syncer) serverCountSince(ctx context.Context, f store.Folder, since time.Time) (int, error) {
	q := url.Values{}
	q.Set("$filter", "receivedDateTime ge "+since.Format(time.RFC3339))
	q.Set("$count", "true")
	q.Set("$top", "1")
	q.Set("$select", "id")
	var out struct {
		Count *int `json:"@odata.count"`
	}
	if err := s.client.Get(ctx, "me/mailFolders/"+url.PathEscape(f.Mailbox)+"/messages?"+q.Encode(), &out); err != nil {
		return 0, err
	}
	if out.Count == nil {
		return 0, errNoCount
	}
	return *out.Count, nil
}
