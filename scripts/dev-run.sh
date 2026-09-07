#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Start the backend in the background, wait for its socket, run the UI, and
# stop the backend when the UI exits.
#
# Usage: scripts/dev-run.sh            (expects ./build/malachid and ./build/malachi)
#        MALACHI_LOG_LEVEL=debug scripts/dev-run.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD="$ROOT/build"

BACKEND="$BUILD/malachid"
UI="$BUILD/malachi"

for bin in "$BACKEND" "$UI"; do
    if [[ ! -x "$bin" ]]; then
        echo "dev-run: missing $bin — run 'make build' first" >&2
        exit 1
    fi
done

# Uninstalled GSettings schema (compiled by `make schemas`). GLib reads the
# variable once at startup; prepend so an existing value keeps working.
SCHEMAS="$BUILD/glib-2.0/schemas"
if [[ -f "$SCHEMAS/gschemas.compiled" ]]; then
    export GSETTINGS_SCHEMA_DIR="$SCHEMAS${GSETTINGS_SCHEMA_DIR:+:$GSETTINGS_SCHEMA_DIR}"
else
    echo "dev-run: $SCHEMAS/gschemas.compiled missing (run 'make build'); settings will not persist" >&2
fi

# Uninstalled translations (compiled by `make locale`).
export MALACHI_LOCALE_DIR="$BUILD/locale"

# Uninstalled application icon: no hicolor theme holds it in a source tree.
export MALACHI_ICON_DIR="$ROOT/ui/data/icons"

# Resolve the socket the same way the daemon does.
if [[ -n "${MALACHI_SOCKET:-}" ]]; then
    SOCK="$MALACHI_SOCKET"
elif [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    SOCK="$XDG_RUNTIME_DIR/malachi/rpc.sock"
else
    SOCK="${XDG_CACHE_HOME:-$HOME/.cache}/malachi/run/rpc.sock"
fi

BACKEND_PID=""
UI_PID=""
cleanup() {
    if [[ -n "$UI_PID" ]] && kill -0 "$UI_PID" 2>/dev/null; then
        kill -TERM "$UI_PID" 2>/dev/null || true
        wait "$UI_PID" 2>/dev/null || true
    fi
    if [[ -n "$BACKEND_PID" ]] && kill -0 "$BACKEND_PID" 2>/dev/null; then
        echo "dev-run: stopping malachid ($BACKEND_PID)"
        kill -TERM "$BACKEND_PID" 2>/dev/null || true
        for _ in $(seq 1 50); do
            kill -0 "$BACKEND_PID" 2>/dev/null || break
            sleep 0.1
        done
        kill -KILL "$BACKEND_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

# If a stale socket exists the daemon replaces it; if a live daemon exists it
# refuses to start and this script exits with its error.
echo "dev-run: starting malachid (socket: $SOCK)"
"$BACKEND" --socket "$SOCK" &
BACKEND_PID=$!

# Wait for the socket (up to 10 s).
for i in $(seq 1 100); do
    if [[ -S "$SOCK" ]]; then
        break
    fi
    if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
        echo "dev-run: malachid exited before creating its socket" >&2
        exit 1
    fi
    sleep 0.1
done
if [[ ! -S "$SOCK" ]]; then
    echo "dev-run: timed out waiting for $SOCK" >&2
    exit 1
fi

echo "dev-run: starting UI"
MALACHI_SOCKET="$SOCK" "$UI" "$@" &
UI_PID=$!
# Wait for the UI; a signal to this script is forwarded to both children by cleanup().
set +e
wait "$UI_PID"
UI_STATUS=$?
set -e
UI_PID=""
exit "$UI_STATUS"
