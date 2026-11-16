// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
)

// extractInWorker is the bridge's Extractor (mcpbridge.Options.Extract):
// it reads one document in a worker, this executable started again with
// extract.WorkerArg (main dispatches it before anything else), and trusts
// nothing it answers (extract.Run). The bridge bounds it with its own
// deadline and its pool of workers.
func extractInWorker(ctx context.Context, f extract.Format, data []byte) (extract.Result, *extract.Refusal, extract.Outcome) {
	exe, err := workerExecutable()
	if err != nil {
		return extract.Result{}, nil, extract.SpawnFailed
	}
	return extract.Run(ctx, exe, f, data, extract.DefaultLimits())
}

// workerExecutable is the worker's executable: this one, as the operating
// system names it (not a PATH lookup), or the file named workerName beside
// it when a package ships one. The Mac App Store build does: its bridge
// runs in a sandbox of its own, and a child of a sandboxed process that
// asks for its own sandbox is ended at start, so the worker there is a
// copy signed to inherit the bridge's (docs/plans/mac-app-store.md). A
// variable, so that a test can make starting a worker fail.
var workerExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return besideWorker(exe), nil
}

// workerName is the optional worker executable beside the bridge.
const workerName = "malachi-mcp-worker"

// besideWorker is workerName in exe's directory (with exe's extension, so
// .exe on Windows) when that is a regular file, else exe itself.
func besideWorker(exe string) string {
	w := filepath.Join(filepath.Dir(exe), workerName+filepath.Ext(exe))
	if info, err := os.Stat(w); err == nil && info.Mode().IsRegular() {
		return w
	}
	return exe
}
