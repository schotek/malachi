// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package api defines the public contract between the Malachi Mail backend
// daemon (malachid) and any user interface talking to it.
//
// The contract is a JSON-RPC 2.0 API carried over a local unix socket. This
// package contains everything a client needs to speak it:
//
//   - wire-level JSON-RPC types (Request, Response, Notification, Error),
//   - method and notification name constants,
//   - request/response parameter types for every method,
//   - the numeric error-code enumeration,
//   - Go interfaces grouping the methods by service.
//
// The human-readable specification lives in docs/api.md. The two must be
// changed together, in the same commit. Anything not documented there is not
// part of the contract.
//
// Design rules (see docs/architecture.md and docs/security.md):
//
//   - Every method touching account data takes an AccountID. Multi-account is
//     not an afterthought.
//   - Every list method is paginated with an opaque cursor. Mailboxes can hold
//     hundreds of thousands of messages.
//   - Message bodies cross this boundary only after sanitisation. There is no
//     method, flag or debugging mode that returns raw HTML.
//   - Error codes are integers from a fixed enumeration, never free-form strings.
package api

// ProtocolVersion is bumped on every incompatible change of the contract.
// Clients compare it against the value returned by system.info.
const ProtocolVersion = 1

// SocketRelPath is the socket location relative to $XDG_RUNTIME_DIR.
const SocketRelPath = "malachi/rpc.sock"
