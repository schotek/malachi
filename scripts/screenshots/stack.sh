#!/bin/sh
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The sample world the screenshots are taken over (docs/screenshots.md):
# malachi-devmail (mail, Jira and eWay-CRM servers in memory with the
# sample set of one language) and malachid over a throwaway data
# directory, with the sample accounts added and the first synchronisation
# waited for. macOS and Linux; the Windows commands are in the document.
#
#   scripts/screenshots/stack.sh start en|cs [DIR]   (re)starts everything
#   scripts/screenshots/stack.sh stop [DIR]          stops it
#
# DIR (default /tmp/malachi-shots) gets bin/ (the Go binaries, built
# here), data/ (the daemon's config, store and mail, wiped by every
# start) and the logs. The socket is MALACHI_SHOTS_SOCKET (default
# DIR/rpc.sock; AF_UNIX paths are short, so DIR must be). The ports are
# SHOTS_IMAP_PORT, SHOTS_SMTP_PORT, SHOTS_CONTROL_PORT, SHOTS_JIRA_PORT and
# SHOTS_EWAY_PORT (11143, 11025, 11080, 11090, 11095). Everything binds to
# 127.0.0.1; nothing here is a real account or a real store.
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CMD=${1:-}
case "$CMD" in
  start) LANG_=${2:-}; DIR=${3:-/tmp/malachi-shots} ;;
  stop) DIR=${2:-/tmp/malachi-shots} ;;
  *) echo "usage: stack.sh start en|cs [DIR] | stack.sh stop [DIR]" >&2; exit 2 ;;
esac
SOCK=${MALACHI_SHOTS_SOCKET:-$DIR/rpc.sock}
IMAP=${SHOTS_IMAP_PORT:-11143}
SMTP=${SHOTS_SMTP_PORT:-11025}
CONTROL=${SHOTS_CONTROL_PORT:-11080}
JIRA=${SHOTS_JIRA_PORT:-11090}
EWAY=${SHOTS_EWAY_PORT:-11095}

stop_stack() {
  if [ -f "$DIR/pids" ]; then
    # shellcheck disable=SC2046
    kill $(cat "$DIR/pids") 2>/dev/null || true
    rm -f "$DIR/pids"
    sleep 1.5
  fi
}

if [ "$CMD" = stop ]; then
  stop_stack
  echo "stack: stopped"
  exit 0
fi

case "$LANG_" in
  en|cs) ;;
  *) echo "stack.sh start: the language is en or cs" >&2; exit 2 ;;
esac

stop_stack
mkdir -p "$DIR/bin" "$(dirname "$SOCK")"
rm -rf "$DIR/data"
mkdir -p "$DIR/data"

# The daemon, the sample servers and the RPC tool, from this tree (the Go
# build cache makes a repeat a no-op).
for bin in malachid malachi-devmail malachi-rpc; do
  (cd "$ROOT/backend" && go build -o "$DIR/bin/$bin" "./cmd/$bin")
done

export MALACHI_DEVMAIL_KEYRING="$DIR/data/keyring.json"
"$DIR/bin/malachi-devmail" serve -lang "$LANG_" -imap-port "$IMAP" -smtp-port "$SMTP" \
  -control-port "$CONTROL" -jira-port "$JIRA" -eway-port "$EWAY" \
  < /dev/null > "$DIR/devmail.out" 2> "$DIR/devmail.err" &
echo $! > "$DIR/pids"
sleep 1

# The daemon's keyring is the sample servers' JSON file (the helper
# protocol of internal/auth/helper), its zone a fixed one so that the
# dates of the set read the same on every machine.
MALACHI_KEYRING=helper MALACHI_KEYRING_HELPER="$DIR/bin/malachi-devmail" \
MALACHI_TIME_ZONE=${MALACHI_TIME_ZONE:-Europe/Prague} MALACHI_LOG_LEVEL=info \
  "$DIR/bin/malachid" --config "$DIR/data/config.toml" --store "$DIR/data/store.db" --socket "$SOCK" \
  < /dev/null > "$DIR/malachid.log" 2>&1 &
echo $! >> "$DIR/pids"
i=0
while [ ! -S "$SOCK" ] || [ ! -f "$SOCK.key" ]; do
  i=$((i + 1))
  [ $i -le 80 ] || { echo "stack: the daemon did not open $SOCK; see $DIR/malachid.log" >&2; exit 1; }
  sleep 0.25
done
sleep 1

# The mail, Jira and eWay-CRM accounts of the sample user; add-account
# also saves the set's saved conversation into the CRM.
"$DIR/bin/malachi-devmail" add-account -socket "$SOCK" -imap-port "$IMAP" -smtp-port "$SMTP" \
  -jira-port "$JIRA" -eway-port "$EWAY" -lang "$LANG_"

# The first synchronisation is done when the board has evaluated the set:
# 17 cases in either language.
want=17
n=0
i=0
while [ "$n" -lt "$want" ]; do
  i=$((i + 1))
  [ $i -le 90 ] || { echo "stack: only $n of $want board cases after 90 s; see $DIR/malachid.log" >&2; exit 1; }
  sleep 1
  n=$("$DIR/bin/malachi-rpc" --socket "$SOCK" --timeout 10s board.list '{}' 2>/dev/null | grep -c '"id": *"c_' || true)
done
echo "stack: ready ($LANG_): $n board cases; socket $SOCK; IMAP $IMAP, SMTP $SMTP, control $CONTROL, Jira $JIRA, eWay $EWAY"
echo "stack: the client runs with MALACHI_DAEMON=none MALACHI_SOCKET=$SOCK and its own MALACHI_DATA_DIR"
