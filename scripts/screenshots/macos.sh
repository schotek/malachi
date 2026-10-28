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
# lands in DIR/app. The app must stay in front while it runs (about two
# minutes): a window of another app over it would be in the shots. Never
# runs the real app in build/ and never its daemon: MALACHI_DAEMON=none
# and the stack's socket.
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
run() {
  rm -rf "$DIR/appdata"; mkdir -p "$DIR/appdata"
  (cd "$OUT" && MALACHI_DAEMON=none MALACHI_SOCKET="$SOCK" MALACHI_DATA_DIR="$DIR/appdata" \
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
  grep -E 'MALACHI_START (row|folder|shot):' "$DIR/app-$LANG_.err" | sed 's/^/  /'
}
# Mail (grouped by conversation), the chat, the invitation, the
# newsletter, a Jira issue, the Board in both styles, the Calendar's week
# and a blank New Message window.
run "group=on;chat=off;row=DevConf;shot=$OUT/inbox.png;row=$QUOTE;chat=on;shot=$OUT/chat.png;chat=off;row=$MEETING;shot=$OUT/invitation.png;row=Design Weekly;shot=$OUT/newsletter.png;folder=Web;row=WEB-1;shot=$OUT/jira.png;board:list:select;shot=$OUT/board-list.png;board:columns;shot=$OUT/board-columns.png;calendar:week;shot=$OUT/calendar-week.png;mail;folder=inbox;row=DevConf;compose-open;shot=$OUT/compose.png;quit"
# eWay-CRM: the saved conversation in Sent and the Save to eWay-CRM sheet
# over the request in the Inbox (a flat list: the Sent copy is one row).
run "group=off;chat=off;folder=sent;row=$QUOTE;shot=$OUT/crm-saved.png;folder=inbox;row=$REQUEST;crm-save;shot=$OUT/crm-dialog.png;quit"
defaults delete $DOMAIN >/dev/null 2>&1 || true
ls "$OUT"/*.png | sed 's/^/macos.sh: /'
