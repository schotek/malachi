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

// SocketRelPath is the socket location relative to the runtime base
// directory that SocketBase returns.
const SocketRelPath = "malachi/rpc.sock"

// SocketBase is the directory the socket path is relative to: the session's
// runtime dir, or inside Flatpak the application's own runtime dir
// $XDG_RUNTIME_DIR/app/$FLATPAK_ID, the only part of the runtime dir that
// every sandbox instance of the application (and the host) sees. The rest
// of the sandbox's runtime dir is a private tmpfs per instance, so a socket
// there would be invisible to a UI started after the one that started the
// daemon. It returns "" when the session has no runtime dir; callers fall
// back to a directory under the cache dir (docs/api.md §1).
//
// Both arguments are the environment variables of the same name; the
// daemon and the UI resolve the path through this function so they agree.
func SocketBase(xdgRuntimeDir, flatpakID string) string {
	if xdgRuntimeDir == "" {
		return ""
	}
	if flatpakID != "" {
		return xdgRuntimeDir + "/app/" + flatpakID
	}
	return xdgRuntimeDir
}
