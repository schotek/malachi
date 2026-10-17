#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# A test malachid on the Mac over a COPY of the user's data, for the GTK
# client in the container (`gtk.sh x11 live` with `gtk.sh forward`). Run it
# from your own Terminal session, so that the keychain is yours: it uses the
# signed keychain helper of the built app, which asks once per item on the
# first run ("Always Allow"). Ctrl+C stops it.
#
#   scripts/gtk-container/test-daemon.sh DATA_COPY [SOCKET]
#
# DATA_COPY holds store.db (make it with `sqlite3 "<data>/store.db"
# ".backup DATA_COPY/store.db"`) and a copy of messages/ and attachments/;
# SOCKET defaults to /tmp/malachi-gtk/rpc.sock (keep it short: 103 bytes).
set -eu
copy=${1:?DATA_COPY}
sock=${2:-/tmp/malachi-gtk/rpc.sock}
repo=$(cd "$(dirname "$0")/../.." && pwd)
helper="$repo/build/Malachi Mail.app/Contents/MacOS/malachi-keychain"
daemon="${MALACHID:-$repo/build/malachid}"
[ -x "$helper" ] || { echo "no keychain helper at $helper (make macos)"; exit 1; }
[ -x "$daemon" ] || { echo "no daemon at $daemon (make backend)"; exit 1; }
mkdir -p "$(dirname "$sock")" && chmod 700 "$(dirname "$sock")"
rm -f "$sock" "$sock.key"
cd "$copy"
exec env MALACHI_KEYRING=helper MALACHI_KEYRING_HELPER="$helper" MALACHI_TIME_ZONE="${MALACHI_TIME_ZONE:-Europe/Prague}" \
  MALACHI_DEFAULT_COMPRESS_STORE=1 MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30 MALACHI_LOG_LEVEL=info \
  "$daemon" -config "$copy/config.toml" -store "$copy/store.db" -socket "$sock"
