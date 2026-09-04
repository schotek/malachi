// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// pushOps sends the account's queued local changes, oldest first. A change
// the server refuses for the message's sake (gone, or a 4xx it will keep
// answering) is retried with backoff and dropped after maxOpAttempts; a
// transient failure (network, throttling, 5xx, token) ends the pass and
// the syncer backs off as a whole.
func (s *Syncer) pushOps(ctx context.Context, byMailbox map[string]store.Folder) error {
	for round := 0; round < 20; round++ {
		ops, err := s.deps.Store.NextOps(ctx, s.account.ID, s.now(), 500)
		if err != nil {
			return storageError(err)
		}
		if len(ops) == 0 {
			return nil
		}
		progressed := false
		for _, op := range ops {
			settled, err := s.pushOp(ctx, op, byMailbox)
			if err != nil {
				return err
			}
			progressed = progressed || settled
		}
		if !progressed {
			return nil
		}
	}
	return nil
}

// pushOp applies one operation. It reports whether the operation left the
// queue (done or dropped); a failed attempt that stays queued reports
// false.
func (s *Syncer) pushOp(ctx context.Context, op store.Op, byMailbox map[string]store.Folder) (bool, error) {
	if op.RemoteID == "" {
		// A row that never had a server identity cannot be addressed.
		s.log.Warn("dropping operation without a remote id", "op", op.ID, "kind", op.Kind)
		return true, s.dropOp(ctx, op.ID)
	}
	msgURL := "me/messages/" + url.PathEscape(op.RemoteID)
	var err error
	switch op.Kind {
	case store.OpFlag:
		body := map[string]any{}
		if hasFlag(op.Set, api.FlagSeen) {
			body["isRead"] = true
		} else if hasFlag(op.Clear, api.FlagSeen) {
			body["isRead"] = false
		}
		if hasFlag(op.Set, api.FlagFlagged) {
			body["flag"] = map[string]string{"flagStatus": "flagged"}
		} else if hasFlag(op.Clear, api.FlagFlagged) {
			body["flag"] = map[string]string{"flagStatus": "notFlagged"}
		}
		if len(body) == 0 {
			return true, s.doneOp(ctx, op.ID) // nothing Graph can express
		}
		err = s.client.Patch(ctx, msgURL, body, nil)
	case store.OpMove:
		target, terr := s.deps.Store.GetFolder(ctx, s.account.ID, op.TargetFolderID)
		if errors.Is(terr, store.ErrNotFound) {
			s.log.Warn("dropping move to a vanished folder", "op", op.ID)
			return true, s.dropOp(ctx, op.ID)
		}
		if terr != nil {
			return false, storageError(terr)
		}
		err = s.client.Post(ctx, msgURL+"/move", map[string]string{"destinationId": target.Mailbox}, nil)
	case store.OpDelete:
		err = s.client.Post(ctx, msgURL+"/permanentDelete", nil, nil)
		var se *StatusError
		if errors.As(err, &se) && (se.Status == http.StatusBadRequest || se.Status == http.StatusMethodNotAllowed || se.Status == http.StatusNotImplemented) {
			err = s.client.Delete(ctx, msgURL)
		}
	default:
		s.log.Warn("dropping operation of unknown kind", "op", op.ID, "kind", op.Kind)
		return true, s.dropOp(ctx, op.ID)
	}
	switch {
	case err == nil:
		return true, s.doneOp(ctx, op.ID)
	case IsNotFound(err):
		s.log.Info("operation target gone on server", "op", op.ID, "kind", op.Kind)
		return true, s.doneOp(ctx, op.ID)
	case isFinalRefusal(err):
		return s.failOp(ctx, op, err)
	}
	return false, err
}

// isFinalRefusal reports a 4xx the service will keep giving for this
// operation (bad request, forbidden, conflict…), as opposed to a
// transient condition.
func isFinalRefusal(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	switch se.Status {
	case http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusRequestTimeout:
		return false
	}
	return se.Status >= 400 && se.Status < 500
}

func (s *Syncer) doneOp(ctx context.Context, id int64) error {
	if err := s.deps.Store.MarkOpDone(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	return nil
}

func (s *Syncer) dropOp(ctx context.Context, id int64) error {
	if err := s.deps.Store.DropOp(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	return nil
}

// failOp records a refused attempt with backoff, dropping the operation
// once maxOpAttempts is reached.
func (s *Syncer) failOp(ctx context.Context, op store.Op, err error) (bool, error) {
	msg := transport.CleanMessage(err.Error())
	if op.Attempts+1 >= maxOpAttempts {
		s.log.Warn("dropping operation after repeated refusals", "op", op.ID, "kind", op.Kind, "err", msg)
		return true, s.dropOp(ctx, op.ID)
	}
	retry := s.now().Add(opBackoff(op.Attempts))
	s.log.Warn("operation refused, will retry", "op", op.ID, "kind", op.Kind, "attempt", op.Attempts+1, "err", msg)
	if err := s.deps.Store.MarkOpFailed(ctx, op.ID, msg, retry); err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, storageError(err)
	}
	return false, nil
}

// opBackoff is opBackoffMin doubled per failed attempt, capped.
func opBackoff(attempts int) time.Duration {
	d := opBackoffMin
	for i := 0; i < attempts && d < opBackoffMax; i++ {
		d *= 2
	}
	return min(d, opBackoffMax)
}

func hasFlag(flags []api.Flag, want api.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
