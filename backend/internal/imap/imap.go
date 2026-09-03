// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package imap contains the IMAP client and the synchronisation engine.
//
// Libraries: github.com/emersion/go-imap/v2 for the protocol,
// github.com/emersion/go-message for MIME parsing.
//
// Before implementing synchronisation, read how Geary (engine/imap-engine)
// and Evolution (camel-imapx) deal with:
//   - servers that violate the RFCs (Exchange, Dovecot quirks, UIDVALIDITY
//     churn, CONDSTORE/QRESYNC absence),
//   - MIME that is malformed, truncated, mislabelled or recursively nested,
//   - threading when References/In-Reply-To are missing or lie.
//
// See docs/architecture.md for the sync model (folder state machine,
// incremental fetch by UID ranges, IDLE for INBOX, backoff on failure).
//
// Every byte received from the server is hostile input. Parsers must be
// exercised with the pathological samples in backend/testdata/mime.
package imap

import (
	"context"

	// Pinned dependencies for this package.
	_ "github.com/emersion/go-imap/v2"
	_ "github.com/emersion/go-message"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Syncer drives synchronisation for one account.
// TODO(phase-1): implement.
type Syncer interface {
	// Run blocks until ctx is cancelled, synchronising on the configured
	// interval and on Trigger.
	Run(ctx context.Context) error
	Trigger(folder api.FolderID, full bool)
	State() api.SyncState
}

// NewSyncer is the bootstrap placeholder.
func NewSyncer(account api.AccountID) (Syncer, error) {
	return nil, api.ErrNotImplemented
}
