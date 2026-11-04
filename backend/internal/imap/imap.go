// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package imap contains the IMAP client and the synchronisation engine.
//
// Libraries: github.com/emersion/go-imap/v2 for the protocol, the charset
// table of github.com/emersion/go-message for encoded words; message
// bodies are parsed by internal/mime.
//
// Layout: probe.go/client.go (connect, login, per-command budgets),
// folders.go (LIST → store folders, roles), folder_sync.go (the one
// per-folder algorithm), ops.go (pushing local flag/move/delete
// operations), sync.go (the per-account actor: Syncer) and supervisor.go
// (one Syncer per enabled account; the core.SyncSupervisor implementation).
//
// See docs/architecture.md §3.2 for the sync model: retention window by
// UID SEARCH SINCE, envelopes before bodies, IDLE on INBOX, backoff on
// failure, local-first mutations through the operation log.
//
// Every byte received from the server is hostile input: strings are
// cleaned and capped, sets are size-checked before they are expanded,
// literals are bounded by the raw-message cap and drained, and the
// parsers are exercised with the samples in backend/testdata/mime.
package imap
