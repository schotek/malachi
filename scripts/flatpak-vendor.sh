#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Vendor the Go dependencies of both modules so the Flatpak build needs no
# network. flatpak-builder runs every module without network access; the
# manifest therefore builds with GOFLAGS=-mod=vendor and GOPROXY=off.
#
# Run this on the host (or in CI) before `make flatpak`. The vendor trees are
# generated, not committed — see .gitignore.
#
# GOWORK=off is required twice over: `go mod vendor` refuses to run in
# workspace mode, and a build in workspace mode ignores the per-module
# vendor/ directories.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for module in backend ui; do
    echo "vendoring $module/…"
    (cd "$ROOT/$module" && GOWORK=off go mod vendor)
done
