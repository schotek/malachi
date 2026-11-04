#!/bin/sh
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The macOS screenshots of docs/screenshots.md: a throwaway app bundle
# (bundle id io.github.schotek.Malachi.shots, so its preferences never
# touch the real app's) from the debug build of the Swift package, put
# through the scenes with MALACHI_START over the sample stack of
# stack.sh, each scene written as a PNG by the app itself (the `shot=`
# step: the window at the screen's scale, no shadow).
#
#   scripts/screenshots/stack.sh start en
#   scripts/screenshots/macos.sh en OUTDIR [DIR]
#
# DIR is stack.sh's directory (default /tmp/malachi-shots); the bundle
# lands in DIR/app. The app must stay in front while it runs (about five
# minutes): a window of another app over it would be in the shots. Never
# runs the real app in build/ and never its daemon: MALACHI_DAEMON=none
# and the stack's socket.
#
# The third run takes the assistant's scenes (docs/screenshots.md §2.1)
# over the demo claude of backend/cmd/malachi-devclaude (stack.sh builds
# it): the real Claude Code is never started, nothing leaves the computer,
# and nothing of the user's own Claude configuration is read or written
# (the run has a throwaway HOME). SHOTS_ONLY=assistant runs that run alone
# (to try a scene); SHOTS_ONLY=mail the first two.
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
LANG_=${1:-}; OUT=${2:-}; DIR=${3:-/tmp/malachi-shots}
case "$LANG_" in
  en|cs) ;;
  *) echo "usage: macos.sh en|cs OUTDIR [DIR]" >&2; exit 2 ;;
esac
[ -n "$OUT" ] || { echo "usage: macos.sh en|cs OUTDIR [DIR]" >&2; exit 2; }
SOCK=${MALACHI_SHOTS_SOCKET:-$DIR/rpc.sock}
[ -S "$SOCK" ] || { echo "macos.sh: no stack at $SOCK; run stack.sh start $LANG_ first" >&2; exit 1; }
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)

# 1. The bundle: the debug build of the app (a release build needs no
# more than this and takes longer), the SDK recorded with vtool as
# macos/Makefile does (AppKit picks the design by it), the icon and the
# .lproj directories through the Makefile's own recipes into DIR/macos,
# Info.plist from the template with the throwaway bundle id, ad hoc
# signed.
APP="$DIR/app/Malachi Mail.app"
CONTENTS="$APP/Contents"
DOMAIN=io.github.schotek.Malachi.shots
(cd "$ROOT/macos" && swift build --product MalachiMail >/dev/null)
BIN=$(cd "$ROOT/macos" && swift build --product MalachiMail --show-bin-path)
make -C "$ROOT/macos" --no-print-directory icon locale BUILD_DIR="$DIR" >/dev/null
rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
cp "$BIN/MalachiMail" "$CONTENTS/MacOS/MalachiMail"
SDK=$(xcrun --show-sdk-version)
vtool -set-build-version macos 14.0 "$SDK" -replace -output "$CONTENTS/MacOS/MalachiMail.sdk" "$CONTENTS/MacOS/MalachiMail"
mv "$CONTENTS/MacOS/MalachiMail.sdk" "$CONTENTS/MacOS/MalachiMail"
cp -R "$DIR"/macos/locale/*.lproj "$CONTENTS/Resources/"
cp "$DIR/macos/Malachi.icns" "$CONTENTS/Resources/Malachi.icns"
VERSION=$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
SHORT=$(printf '%s' "$VERSION" | sed -nE 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/p')
sed -e "s/@VERSION@/${SHORT:-0.0.0}/g" -e "s/@FULL_VERSION@/$VERSION-shots/g" \
    -e "s/@BUNDLE_ID@/$DOMAIN/g" -e 's/@EXECUTABLE@/MalachiMail/g' \
    -e 's|@LOCALIZATIONS@|<string>en</string><string>cs</string>|g' \
    "$ROOT/macos/Resources/Info.plist.in" > "$CONTENTS/Info.plist"
plutil -lint "$CONTENTS/Info.plist" >/dev/null
printf 'APPL????' > "$CONTENTS/PkgInfo"
codesign --force --sign - "$APP" >/dev/null 2>&1
echo "macos.sh: bundle $APP ($VERSION, SDK $SDK)"

# 2. The app's language and region, in the throwaway domain.
defaults delete $DOMAIN >/dev/null 2>&1 || true
defaults write $DOMAIN AppleLanguages -array "$LANG_"
defaults write $DOMAIN AppleLocale -string "$([ "$LANG_" = cs ] && echo cs_CZ || echo en_GB)"

# 3. The scenes. The subjects and folders are the sample set's (server
# folder names; a substring of the subject, case-insensitively); the
# window is 1440×900 points, so a Retina screen gives 2880×1800 pixels.
case "$LANG_" in
  en) QUOTE=Quotation; MEETING='Q4 planning'; REQUEST='Request for a quote' ;;
  cs) QUOTE=Nabídka; MEETING=Porada; REQUEST=Poptávka ;;
esac
# The display stays awake while the app runs: a sleeping display leaves
# new windows unrendered (blank pictures) and the editor's WebKit idle.
caffeinate -u -t 2 2>/dev/null || true
caffeinate -d -i -w $$ 2>/dev/null &
# RUN_HOME is the app's HOME (the third run gets a throwaway one).
RUN_HOME=$HOME
run() {
  rm -rf "$DIR/appdata"; mkdir -p "$DIR/appdata"
  (cd "$OUT" && HOME="$RUN_HOME" MALACHI_DAEMON=none MALACHI_SOCKET="$SOCK" MALACHI_DATA_DIR="$DIR/appdata" \
    MALACHI_CALENDAR_SAMPLES=1 MALACHI_START="$1" MALACHI_START_SIZE="${SIZE:-1440x900}" \
    MALACHI_START_INTERVAL="${INTERVAL:-4}" "$CONTENTS/MacOS/MalachiMail" 2> "$DIR/app-$LANG_.err") &
  pid=$!
  # The run ends with the app's quit step; a hung app is killed.
  i=0
  while kill -0 $pid 2>/dev/null; do
    i=$((i + 1)); [ $i -le "${WATCHDOG:-300}" ] || { echo "macos.sh: the app did not quit, killing it" >&2; kill $pid; }
    sleep 1
  done
  wait $pid 2>/dev/null || true
  grep -E 'MALACHI_START (row|folder|shot|assistant[-a-z]*|board-select|triage|suggest-reply|compose-[a-z]*|reply|search-own|prefs-ai):' "$DIR/app-$LANG_.err" | sed 's/^/  /'
}
if [ "${SHOTS_ONLY:-}" != assistant ]; then
# Mail (grouped by conversation), the chat, the invitation, the
# newsletter, a Jira issue, the Board in both styles, the Calendar's week
# and a blank New Message window.
run "group=on;chat=off;row=DevConf;shot=$OUT/inbox.png;row=$QUOTE;chat=on;shot=$OUT/chat.png;chat=off;row=$MEETING;shot=$OUT/invitation.png;row=Design Weekly;shot=$OUT/newsletter.png;folder=Web;row=WEB-1;shot=$OUT/jira.png;board:list:select;shot=$OUT/board-list.png;board:columns;shot=$OUT/board-columns.png;calendar:week;shot=$OUT/calendar-week.png;mail;folder=inbox;row=DevConf;compose-open;shot=$OUT/compose.png;quit"
# eWay-CRM: the saved conversation in Sent and the Save to eWay-CRM sheet
# over the request in the Inbox (a flat list: the Sent copy is one row).
run "group=off;chat=off;folder=sent;row=$QUOTE;shot=$OUT/crm-saved.png;folder=inbox;row=$REQUEST;crm-save;shot=$OUT/crm-dialog.png;quit"
fi
if [ "${SHOTS_ONLY:-}" != mail ]; then
# 4. The assistant's scenes, over the demo claude. Only now does the
# bundle get the bridge (the mail scenes above show no Assistant without
# it), and the app runs with a throwaway HOME whose Claude Desktop and
# Claude Code configuration files register the bundle's bridge: "Register
# with Claude" reads as on, and the user's own ~/.claude.json and Claude
# Desktop file are never touched. The preferences of the assistant go
# into the throwaway domain; the profile and the board's preference into
# the stack's daemon (stack.sh assistant).
BRIDGE="$CONTENTS/MacOS/malachi-mcp"
cp "$DIR/bin/malachi-mcp" "$BRIDGE"
codesign --force --sign - "$APP" >/dev/null 2>&1
RUN_HOME=$DIR/home
rm -rf "$RUN_HOME"
mkdir -p "$RUN_HOME/Library/Application Support/Claude" "$RUN_HOME/.claude" "$RUN_HOME/Library/Caches/Malachi Mail/assistant"
# The bridge compares the registered command with its own path resolved
# (/private/tmp for /tmp): the entry names that one.
ENTRY=$(printf '{"mcpServers":{"malachi":{"type":"stdio","command":"%s","args":[]}}}' "$(cd "$CONTENTS/MacOS" && pwd -P)/malachi-mcp")
printf '%s\n' "$ENTRY" > "$RUN_HOME/Library/Application Support/Claude/claude_desktop_config.json"
printf '%s\n' "$ENTRY" > "$RUN_HOME/.claude.json"
"$ROOT/scripts/screenshots/stack.sh" assistant "$LANG_" "$DIR"
defaults write $DOMAIN assistant-menu -bool true
defaults write $DOMAIN assistant-target -string app
defaults write $DOMAIN assistant-provider -string claude
defaults write $DOMAIN assistant-consent -bool true
defaults write $DOMAIN board-triage-consent -bool true
# The demo under the native installer's name and place in the throwaway
# home (the Claude Code row of Settings → AI shows the path).
mkdir -p "$RUN_HOME/.local/bin"
ln -s "$DIR/bin/malachi-devclaude" "$RUN_HOME/.local/bin/claude"
defaults write $DOMAIN assistant-claude-path -string "$RUN_HOME/.local/bin/claude"
defaults write $DOMAIN assistant-model -string sonnet
defaults write $DOMAIN board-triage-model -string sonnet
# The words of the scenes: what the user types into the panel, the compose
# window, the assistant's bar and the search field (the demo claude knows
# them, answers/en.json and cs.json).
case "$LANG_" in
  en)
    PRESENTATION='Board presentation'
    DRAFT_WORDS='Confirm the lighting as a third section and the complete quotation on Friday by noon, and ask whether the extended warranty should be 24 or 36 months.'
    WRITE_WORDS='Thank him for the drawing, confirm the belt section and the lighting, and ask about the warranty length'
    COMPOSE='Eva Králová <eva.kralova@bluebird.test>|Site visit next week|Hi, I got your request. Tuesday afternoon is the only time I have next week, 2 pm at your place in Karlín. Send me the address and the floor plan before that, otherwise there is no point in coming.\nJana'
    SEARCH_WORDS='mail from Petr about the warehouse' ;;
  cs)
    PRESENTATION='Prezentace pro představenstvo'
    DRAFT_WORDS='Potvrď osvětlení jako třetí část a kompletní nabídku v pátek do poledne a zeptej se, jestli má být prodloužená záruka na 24, nebo 36 měsíců.'
    WRITE_WORDS='Poděkuj za výkres, potvrď pásovou část a osvětlení a zeptej se na délku záruky'
    COMPOSE='Eva Králová <eva.kralova@bluebird.test>|Návštěva místa příští týden|Dobrý den, poptávku jsem dostala. Příští týden mám čas jedině v úterý odpoledne, ve 14:00 u vás v Karlíně. Předtím mi pošlete adresu a půdorys, jinak nemá smysl jezdit.\nJana'
    SEARCH_WORDS='pošta od Petra o skladu' ;;
esac
# The search in the user's own words (first: the panel, once open, folds
# the toolbar's field into its overflow), the panel over the Inbox
# (Summarize Unread in This Folder), a new conversation over the quotation
# thread (Tasks and Deadlines, then Draft a Reply… with the words and the
# Open Draft card), the Board's triage run with the board presentation
# selected, its suggested reply, a reply with the assistant's bar, a new
# message with the ✦ popover (More Polite), and Settings → AI (as tall as
# the screen, scrolled by PREFS_SCROLL points, 140: the profile's text,
# the Assistant group and the start of the Board group). SHOTS_STEPS runs that
# script instead (to try a scene; `shot=` wants absolute paths).
run "${SHOTS_STEPS:-group=on;chat=off;folder=inbox;search-own=$SEARCH_WORDS;wait=8;shot=$OUT/search-ownwords.png;search-clear;folder=inbox;row=$REQUEST;assistant-unread;wait=10;shot=$OUT/assistant-panel.png;assistant-new;row=$QUOTE;assistant=tasks;wait=8;assistant=draft-reply;assistant-say=$DRAFT_WORDS;wait=10;shot=$OUT/assistant-draft.png;board:list;board-select=$PRESENTATION;triage;wait=30;shot=$OUT/board-triage.png;suggest-reply;wait=10;shot=$OUT/board-reply.png;mail;row=$QUOTE;reply;wait=5;compose-write=$WRITE_WORDS;wait=10;shot=$OUT/compose-write.png;compose-new=$COMPOSE;wait=5;compose-rewrite=politer;wait=8;shot=$OUT/compose-rewrite.png;prefs-ai=${PREFS_SCROLL:-140};wait=4;shot=$OUT/prefs-ai.png;quit}"
rm -rf "$RUN_HOME"
fi
defaults delete $DOMAIN >/dev/null 2>&1 || true
ls "$OUT"/*.png | sed 's/^/macos.sh: /'
