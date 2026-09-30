// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"errors"
	"sort"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Local operations. The flags of an issue-tracker account live on this
// device only (Jira has no read state to write back), but an item has a
// copy in its space's folder and in every view the issue belongs to, and
// the user flags one of them: message.flag queues a flag operation like
// on any account, and the syncer gives every copy of the item the flags
// that copy has, then drops the operation. Moves and deletions cannot be
// asked for (the account has neither capability); one that is queued all
// the same is dropped.

// pushOps applies the account's queued operations, oldest first; it needs
// no network. Each flag operation is applied again as the change it was
// (set and clear) on top of what its message has by then, so that of two
// changes to two copies of an item the later one wins everywhere.
func (s *Syncer) pushOps(ctx context.Context) error {
	for round := 0; round < 20; round++ {
		ops, err := s.deps.Store.NextOps(ctx, s.account.ID, s.now(), 500)
		if err != nil {
			return storageError(err)
		}
		if len(ops) == 0 {
			return nil
		}
		for _, op := range ops {
			if err := s.pushOp(ctx, op); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Syncer) pushOp(ctx context.Context, op store.Op) error {
	if op.Kind != store.OpFlag {
		s.log.Info("dropping an operation the account cannot take", "op", op.ID, "kind", op.Kind)
		return s.settleOp(ctx, s.deps.Store.DropOp(ctx, op.ID))
	}
	m, err := s.deps.Store.GetMessage(ctx, s.account.ID, op.MessageID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return s.settleOp(ctx, s.deps.Store.MarkOpDone(ctx, op.ID))
	case err != nil:
		return storageError(err)
	}
	remoteID := m.RemoteID
	if remoteID == "" {
		remoteID = op.RemoteID
	}
	if remoteID != "" {
		flags := applyFlags(m.Flags, op.Set, op.Clear)
		if _, err := s.deps.Store.SetFlagsByRemoteID(ctx, s.account.ID, remoteID, flags); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	return s.settleOp(ctx, s.deps.Store.MarkOpDone(ctx, op.ID))
}

// settleOp treats an operation already gone as settled.
func (s *Syncer) settleOp(_ context.Context, err error) error {
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	return nil
}

// applyFlags is flags with set added and clear removed, sorted.
func applyFlags(flags, set, clear []api.Flag) []api.Flag {
	drop := map[api.Flag]bool{}
	for _, f := range clear {
		drop[f] = true
	}
	seen := map[api.Flag]bool{}
	var out []api.Flag
	for _, f := range append(append([]api.Flag{}, flags...), set...) {
		if f == "" || drop[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
