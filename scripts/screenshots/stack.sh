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
#   scripts/screenshots/stack.sh assistant en|cs [DIR]  prepares the assistant's scenes
#   scripts/screenshots/stack.sh stop [DIR]          stops it
#
# `assistant` is run after `start` for the assistant's scenes
# (docs/screenshots.md §2.1): it writes the sample user's profile for the
# assistant (assistant.profileSet: a text, the gender, a note per account)
# and turns the board's assistant preference on with the mail and the Jira
# account as the triage accounts (board.setPreferences), which the clients
# do themselves on the user's consent; the client's own consent keys are
# the platform's business. It is idempotent.
#
# DIR (default /tmp/malachi-shots) gets bin/ (the Go binaries, built
# here: the daemon, the sample servers, the RPC tool, the MCP bridge and
# the demo claude of backend/cmd/malachi-devclaude), data/ (the daemon's
# config, store and mail, wiped by every start) and the logs. The socket is MALACHI_SHOTS_SOCKET (default
# DIR/rpc.sock; AF_UNIX paths are short, so DIR must be). The ports are
# SHOTS_IMAP_PORT, SHOTS_SMTP_PORT, SHOTS_CONTROL_PORT, SHOTS_JIRA_PORT and
# SHOTS_EWAY_PORT (11143, 11025, 11080, 11090, 11095). Everything binds to
# 127.0.0.1; nothing here is a real account or a real store.
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CMD=${1:-}
case "$CMD" in
  start|assistant) LANG_=${2:-}; DIR=${3:-/tmp/malachi-shots} ;;
  stop) DIR=${2:-/tmp/malachi-shots} ;;
  *) echo "usage: stack.sh start en|cs [DIR] | stack.sh assistant en|cs [DIR] | stack.sh stop [DIR]" >&2; exit 2 ;;
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
  *) echo "stack.sh $CMD: the language is en or cs" >&2; exit 2 ;;
esac

# The sample user's profile for the assistant and the board's assistant
# preference, over a running stack. The texts are the sample world's (the
# user of samples/en.json and cs.json) and nothing else's; python3 only
# assembles the JSON.
if [ "$CMD" = assistant ]; then
  [ -S "$SOCK" ] || { echo "stack.sh assistant: no stack at $SOCK; run stack.sh start $LANG_ first" >&2; exit 1; }
  RPC="$DIR/bin/malachi-rpc"
  "$RPC" --socket "$SOCK" account.list '{}' > "$DIR/accounts.json"
  "$RPC" --socket "$SOCK" board.preferences '{}' > "$DIR/board-prefs.json"
  python3 - "$LANG_" "$DIR" <<'PY' > "$DIR/assistant-params.json"
import json, sys
lang, d = sys.argv[1], sys.argv[2]
accounts = json.load(open(d + "/accounts.json"))["accounts"]
mail = next(a["id"] for a in accounts if a["config"].get("kind", "imap") in ("imap", "graph"))
jira = next((a["id"] for a in accounts if a["config"].get("kind") == "jira"), None)
if lang == "cs":
    text = ("Mám na starosti obchod a projekty v Kovar Engineering, rodinné firmě na ocelové konstrukce: "
            "regály, mezipatra a dopravníky pro sklady a dílny. Píšu stručně a přátelsky. "
            "Zákazníkům vykám, kolegům tykám.")
    notes = {mail: "Kolegům se podepisuj jen Jana, zákazníkům Jana Dvořáková.",
             jira: "Komentáře krátké a věcné; Martinovi a Tomášovi tykej."}
else:
    text = ("I run the sales and project side of Kovar Engineering, a family steel-construction firm: "
            "racking, mezzanines and conveyors for warehouses and workshops. I write briefly and in a friendly tone. "
            "Customers get a formal register, colleagues their first names.")
    notes = {mail: "Sign as Jana (first name only) to colleagues and as Jana Dvořáková to customers.",
             jira: "Comments short and factual; Martin and Tomáš by their first names."}
profile = {"text": text, "gender": "feminine",
           "accounts": [{"accountId": k, "note": v} for k, v in notes.items() if k]}
prefs = json.load(open(d + "/board-prefs.json"))["preferences"]
prefs["assistant"] = True
prefs["triageAccounts"] = [a for a in (mail, jira) if a]
print(json.dumps({"profile": profile, "preferences": prefs}, ensure_ascii=False))
PY
  python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["profile"], ensure_ascii=False))' "$DIR/assistant-params.json"     | "$RPC" --socket "$SOCK" assistant.profileSet - > /dev/null
  python3 -c 'import json,sys; print(json.dumps({"preferences": json.load(open(sys.argv[1]))["preferences"]}, ensure_ascii=False))' "$DIR/assistant-params.json"     | "$RPC" --socket "$SOCK" board.setPreferences - > /dev/null
  rm -f "$DIR/accounts.json" "$DIR/board-prefs.json" "$DIR/assistant-params.json"
  echo "stack: the assistant's profile ($LANG_) is set and the board's assistant preference is on (mail and Jira triaged)"
  exit 0
fi

stop_stack
mkdir -p "$DIR/bin" "$(dirname "$SOCK")"
rm -rf "$DIR/data"
mkdir -p "$DIR/data"

# The daemon, the sample servers, the RPC tool, the MCP bridge and the demo
# claude (the assistant's scenes), from this tree (the Go build cache makes
# a repeat a no-op).
for bin in malachid malachi-devmail malachi-rpc malachi-mcp malachi-devclaude; do
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
