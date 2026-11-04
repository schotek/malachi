// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type outboxService struct{ b *Backend }

// Retry re-queues a failed outbox message, or makes a queued one due at
// once, and wakes the account's worker (docs/api.md §4.3 outbox.retry).
func (s *outboxService) Retry(ctx context.Context, p api.OutboxRetryParams) (*api.OutboxRetryResult, error) {
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	err = s.b.store.RetryOutbox(ctx, a.ID, string(p.MessageID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeMessageNotFound, "unknown outbox message %q", p.MessageID)
	case errors.Is(err, store.ErrOutboxBusy):
		return nil, api.NewError(api.CodeConflict, "message is being sent")
	case errors.Is(err, store.ErrOutbox):
		return nil, api.NewError(api.CodeInvalidArgument, "message already delivered")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if !s.b.Delivery.Wake(a.ID) {
		s.b.log.Debug("outbox retry without a running worker", "account", a.ID)
	}
	s.b.outboxChanged(a.ID)
	return &api.OutboxRetryResult{}, nil
}

// toAPIOutbox is the wire form of an outbox entry (MessageSummary.outbox).
func toAPIOutbox(e store.OutboxEntry) *api.OutboxInfo {
	info := &api.OutboxInfo{State: api.OutboxState(e.State), Attempts: e.Attempts}
	if !e.NextAttemptAt.IsZero() && (e.State == store.OutboxQueued || e.NextAttemptAt.After(time.Now())) {
		at := e.NextAttemptAt
		info.NextAttemptAt = &at
	}
	if e.LastErrorCode != 0 {
		info.Error = &api.Error{Code: e.LastErrorCode, Message: e.LastError}
	}
	return info
}
