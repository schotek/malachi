#!/bin/sh
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The Linux (GTK) screenshots of docs/screenshots.md: the client from
# BUILD_DIR on its own Xvfb display (2880x1800, GDK_SCALE=2, so the
# 1440x900 window fills it; no window manager, no compositor), put through
# the scenes with MALACHI_GTK_START over the sample stack of stack.sh.
# Each `shot=` step prints "MALACHI_GTK_START shot <path>"; this script
# answers it with the X server's image of the topmost window (xwininfo,
# xwd, xwd2png.py), unscaled.
#
#   scripts/screenshots/stack.sh start en
#   scripts/screenshots/gtk.sh en OUTDIR [DIR]
#
# Needs Xvfb, xwininfo, xwd, xdotool and dbus-daemon on PATH (the Fedora
# container has Xvfb; the others come with xwd, xwininfo, xdotool and
# dbus-daemon). DIR is stack.sh's directory (default /tmp/malachi-shots).
# The client never sees the user's own data: MALACHI_DAEMON=none, the
# stack's socket, XDG directories and a session bus of its own under
# DIR/gtk, GSettings in a keyfile there. SHOTS_DISPLAY (default :99),
# SHOTS_INTERVAL (seconds per step, default 4), BUILD_DIR (default
# build/linux), LOCPATH when the locales are not installed; SHOTS_STEPS
# runs that one script instead of the scenes (to try a step).
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
LANG_=${1:-}; OUT=${2:-}; DIR=${3:-/tmp/malachi-shots}
case "$LANG_" in
  en) LOCALE=en_GB.UTF-8; NAB=Quotation; Q4="Q4 planning"; REQ="Request for a quote" ;;
  cs) LOCALE=cs_CZ.UTF-8; NAB=Nabídka; Q4=Porada; REQ=Poptávka ;;
  *) echo "usage: gtk.sh en|cs OUTDIR [DIR]" >&2; exit 2 ;;
esac
[ -n "$OUT" ] || { echo "usage: gtk.sh en|cs OUTDIR [DIR]" >&2; exit 2; }
SOCK=${MALACHI_SHOTS_SOCKET:-$DIR/rpc.sock}
[ -S "$SOCK" ] || { echo "gtk.sh: no stack at $SOCK; run stack.sh start $LANG_ first" >&2; exit 1; }
BUILD=$ROOT/${BUILD_DIR:-build/linux}
[ -x "$BUILD/malachi" ] || { echo "gtk.sh: no $BUILD/malachi; run make build first" >&2; exit 1; }
DPY=${SHOTS_DISPLAY:-:99}
INTERVAL=${SHOTS_INTERVAL:-4}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)

# A throwaway home for the client: XDG directories, the GSettings
# keyfile (the window's size) and the look of a GNOME desktop (the block
# gtk.sh x11 writes; light, the default colour scheme).
HOME_=$DIR/gtk
rm -rf "$HOME_"
mkdir -p "$HOME_/config/glib-2.0/settings" "$HOME_/config/gtk-4.0" "$HOME_/data" "$HOME_/cache" "$HOME_/run"
chmod 700 "$HOME_/run"
printf "[io/github/schotek/Malachi]\nwindow-width=1440\nwindow-height=900\n" > "$HOME_/config/glib-2.0/settings/keyfile"
printf "[Settings]\ngtk-font-name=Adwaita Sans 11\ngtk-icon-theme-name=Adwaita\ngtk-cursor-theme-name=Adwaita\ngtk-cursor-theme-size=24\ngtk-xft-antialias=1\ngtk-xft-hinting=1\ngtk-xft-hintstyle=hintslight\ngtk-xft-rgba=rgb\ngtk-hint-font-metrics=1\n" > "$HOME_/config/gtk-4.0/settings.ini"

# The display and a session bus of its own.
Xvfb "$DPY" -screen 0 2880x1800x24 -nolisten tcp > "$HOME_/xvfb.log" 2>&1 &
XVFB=$!
BUS=$(dbus-daemon --session --fork --print-address=1 --print-pid=1 --address="unix:path=$HOME_/run/bus")
BUS_PID=$(echo "$BUS" | sed -n 2p)
trap 'kill $XVFB $BUS_PID 2>/dev/null || true' EXIT INT TERM
sleep 1
# The pointer in the screen's corner, where it hovers over nothing.
DISPLAY=$DPY xdotool mousemove 2879 1799

run() {
  log=$HOME_/run-$1.log
  env -i PATH="$PATH" HOME="$HOME_" LANG="$LOCALE" LC_ALL="$LOCALE" ${LOCPATH:+LOCPATH="$LOCPATH"} TZ=Europe/Prague \
    DISPLAY="$DPY" GDK_BACKEND=x11 GDK_SCALE=2 GSK_RENDERER="${SHOTS_GSK_RENDERER:-cairo}" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=$HOME_/run/bus" \
    XDG_CONFIG_HOME="$HOME_/config" XDG_DATA_HOME="$HOME_/data" XDG_CACHE_HOME="$HOME_/cache" XDG_RUNTIME_DIR="$HOME_/run" \
    GSETTINGS_SCHEMA_DIR="$BUILD/glib-2.0/schemas" GSETTINGS_BACKEND=keyfile MALACHI_LOCALE_DIR="$BUILD/locale" \
    ${WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS:+WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1} \
    MALACHI_DAEMON=none MALACHI_SOCKET="$SOCK" MALACHI_CALENDAR_SAMPLES=1 MALACHI_LOG_LEVEL=info \
    MALACHI_GTK_START="$2" MALACHI_GTK_START_INTERVAL="$INTERVAL" \
    "$BUILD/malachi" 2>&1 | tee "$log" | while IFS= read -r line; do
      case "$line" in
        "MALACHI_GTK_START shot "*)
          path=${line#MALACHI_GTK_START shot }
          sleep 1
          DISPLAY=$DPY python3 "$ROOT/scripts/screenshots/xwd2png.py" top "$path" && echo "gtk.sh: $path" ;;
        "MALACHI_GTK_START "*) echo "$line" ;;
      esac
    done
}

if [ -n "${SHOTS_STEPS:-}" ]; then
  run try "$SHOTS_STEPS"
  exit 0
fi
run main "group=on;chat=off;folder=inbox;row=DevConf;shot=$OUT/inbox.png;row=$NAB;chat=on;shot=$OUT/chat.png;\
chat=off;row=$Q4;shot=$OUT/invitation.png;row=Design Weekly;shot=$OUT/newsletter.png;\
folder=Web;row=WEB-1;shot=$OUT/jira.png;board:list:select;shot=$OUT/board-list.png;\
board:columns:select;shot=$OUT/board-columns.png;calendar:week;shot=$OUT/calendar-week.png;\
mail;folder=inbox;row=DevConf;compose-open;shot=$OUT/compose.png;quit"
run crm "group=off;chat=off;folder=sent;row=$NAB;shot=$OUT/crm-saved.png;\
folder=inbox;row=$REQ;crm-save;shot=$OUT/crm-dialog.png;quit"
echo "gtk.sh: $(ls "$OUT"/*.png | wc -l) pictures in $OUT"
