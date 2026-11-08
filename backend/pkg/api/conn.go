// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "context"

// ConnID identifies one authenticated RPC connection for the daemon's
// lifetime. The RPC server puts it into the context of every call it
// dispatches (WithConn); a service that keeps per-connection state (the
// assistant's sessions, §4.18) reads it with ConnFromContext and the
// server tells it when the connection closes (ConnObserver). Clients never
// see it.
type ConnID string

type connKey struct{}

// WithConn returns ctx carrying the connection id.
func WithConn(ctx context.Context, id ConnID) context.Context {
	return context.WithValue(ctx, connKey{}, id)
}

// ConnFromContext returns the connection id of a dispatched call, or
// false for a context without one (a test, an internal call).
func ConnFromContext(ctx context.Context) (ConnID, bool) {
	id, ok := ctx.Value(connKey{}).(ConnID)
	return id, ok && id != ""
}
