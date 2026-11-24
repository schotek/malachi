// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"io"
	"log/slog"
)

// stopOnClose returns a context that ends when r reaches end of file or
// fails, and the daemon with it: --exit-on-stdin-close, where the app that
// starts the daemon holds the other end of a pipe on its standard input for
// as long as it runs. Whatever ends the app, a quit, a crash or a kill,
// closes that end, so the daemon never outlives the app that started it.
// Anything written to the pipe is read and dropped.
func stopOnClose(parent context.Context, r io.Reader, log *slog.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		_, err := io.Copy(io.Discard, r)
		if ctx.Err() == nil {
			if err != nil {
				log.Info("standard input failed; stopping", "err", err)
			} else {
				log.Info("standard input closed; stopping")
			}
		}
		cancel()
	}()
	return ctx, cancel
}
