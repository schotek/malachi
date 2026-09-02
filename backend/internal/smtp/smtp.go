// Package smtp sends messages and manages the outbox queue.
//
// Library: github.com/emersion/go-smtp (client side only).
//
// Model: message.send moves a draft into the local outbox folder; a worker
// delivers queued messages with retry/backoff, then appends the sent copy to
// the account's Sent folder via IMAP. Failures surface through
// notify.syncState for the outbox and never lose the message.
//
// After a successful delivery the worker records every recipient address in
// the known-senders allow-list (store.AddKnownSender with source "sent"),
// which feeds the "knownSenders" remote-content policy (internal/core).
package smtp

import (
	"context"

	// Pinned dependency for this package.
	_ "github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Sender delivers one already-serialised RFC 5322 message.
// TODO(phase-3): implement.
type Sender interface {
	Send(ctx context.Context, account api.AccountID, from string, to []string, raw []byte) error
}

// Outbox is the persistent queue. TODO(phase-3): implement over internal/store.
type Outbox interface {
	Enqueue(ctx context.Context, account api.AccountID, draft api.DraftID) (api.MessageID, error)
	Pending(ctx context.Context, account api.AccountID) (int, error)
}
