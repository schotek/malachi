#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Build and test the GTK client inside the malachi-gtk:f42 container
# (scripts/gtk-container/Containerfile) over this checkout, with the Go module
# cache, the Go build cache and ccache kept on the host, so that gotk4 is
# compiled once. Meant for machines without GTK (a Mac with Apple's
# `container` CLI); the Toolbx stays the primary environment.
#
#   scripts/gtk-container/gtk.sh build            build the image (once; the run modes do it when missing)
#   scripts/gtk-container/gtk.sh make build       any command runs in /src
#   scripts/gtk-container/gtk.sh test-ui          GTK UI tests under Xvfb
#   scripts/gtk-container/gtk.sh shell            an interactive shell
#   scripts/gtk-container/gtk.sh xhost            allow the container bridge on XQuartz
#   scripts/gtk-container/gtk.sh x11              the app in an XQuartz window over sample data
#   scripts/gtk-container/gtk.sh x11 dev          … with its own daemon and gnome-keyring in the
#                                                 container, data in $MALACHI_GTK_CACHE/data
#   scripts/gtk-container/gtk.sh x11 live         … over a daemon on the Mac (see forward)
#   scripts/gtk-container/gtk.sh forward [SOCKET] host side of `x11 live`: forwards the
#                                                 daemon's unix socket to the container
#   scripts/gtk-container/gtk.sh stop             stop Apple's container service
#
# `make run-gtk` is `make build` here, `x11 dev` and `stop`; `make
# run-gtk-live` the same with `x11 live`.
#
# XQuartz: install it (brew install --cask xquartz), once `defaults write
# org.xquartz.X11 nolisten_tcp -bool false` and restart XQuartz (it then
# listens on TCP 6000). Every `x11` mode starts XQuartz, checks that and
# authorises the container through the display's MIT-MAGIC-COOKIE-1 (from
# `xauth list :0` on the Mac, added inside the container as the cookie of
# the gateway's display); only without a cookie it falls back to allowing
# the container bridge with xhost (`gtk.sh xhost`, also for manual use).
# `x11 dev` keeps the store, config, preferences and the keyring (a login
# keyring with the development password "malachi") in
# ~/.cache/malachi-gtk/data across runs; accounts are added in the wizard.
# A browser sign-in (Microsoft 365) opens GNOME Web from the image in its own
# XQuartz window, as a child of the app with its environment; its redirect
# lands on the container's 127.0.0.1, where the daemon listens.
# `x11 live` expects `gtk.sh forward /path/to/rpc.sock` running on the Mac for
# the daemon to test against (its PATH.key is read at start, so restart the
# client after the daemon); `test-daemon.sh` starts such a daemon over a copy
# of the user's data from the user's own Terminal.
#
# A `make` runs with BUILD_DIR=build/linux, so the Linux binaries, schemas
# and locale never land next to the macOS or Windows products in build/.
#
# Variables: ENGINE (container | docker | podman; default container),
# CPUS/MEMORY for Apple's container (8 / 8G), PUBLISH=<port> publishes a port
# (a Broadway display), MALACHI_GTK_CACHE (default ~/.cache/malachi-gtk).
# With Apple's container, stop the service afterwards: gtk.sh stop.
set -eu
repo=$(cd "$(dirname "$0")/../.." && pwd)
engine=${ENGINE:-container}
image=malachi-gtk:f42
cache=${MALACHI_GTK_CACHE:-$HOME/.cache/malachi-gtk}
host_socket=${HOST_SOCKET:-/tmp/malachi-gtk/rpc.sock}
xhost=/opt/X11/bin/xhost
xauth=/opt/X11/bin/xauth
# The display's cookie for the container (x11_preflight); empty with the
# xhost fallback.
cookie=

# allow_bridge lets the container bridge (192.168.64.0/24) on XQuartz's
# local display; xhost takes single addresses. Right after XQuartz starts,
# calls through its launchd socket fail for a few seconds while startx
# still runs, so the loop is repeated until the list holds the container's
# usual address (192.168.64.2) or 10 s passed.
allow_bridge() {
  start=$(date +%s)
  while :; do
    for i in $(seq 2 254); do DISPLAY=:0 "$xhost" "+192.168.64.$i" >/dev/null 2>&1; done
    if DISPLAY=:0 "$xhost" 2>/dev/null | grep -qx 'INET:192.168.64.2'; then
      return 0
    fi
    if [ $(($(date +%s) - start)) -ge 10 ]; then
      echo "xhost: XQuartz did not take the container bridge (no INET:192.168.64.2 in its list)" >&2
      exit 2
    fi
    sleep 1
  done
}

# x11_preflight readies XQuartz on the Mac for the container's client:
# running, listening on TCP 6000 and allowing the bridge. It never changes
# XQuartz's settings nor restarts it.
x11_preflight() {
  if [ ! -x "$xhost" ]; then
    echo "XQuartz is not installed: brew install --cask xquartz" >&2
    exit 2
  fi
  if [ "$(defaults read org.xquartz.X11 nolisten_tcp 2>/dev/null || echo 1)" != 0 ]; then
    echo "XQuartz does not accept TCP connections. Once:" >&2
    echo "  defaults write org.xquartz.X11 nolisten_tcp -bool false" >&2
    echo "then quit XQuartz (if it runs) and run this again." >&2
    exit 2
  fi
  open -a XQuartz
  n=0
  until DISPLAY=:0 "$xhost" >/dev/null 2>&1; do
    n=$((n + 1))
    if [ "$n" -ge 40 ]; then
      echo "XQuartz did not answer on DISPLAY=:0 within 20 s" >&2
      exit 2
    fi
    sleep 0.5
  done
  if ! nc -z -w 1 127.0.0.1 6000 >/dev/null 2>&1; then
    echo "XQuartz is not listening on TCP 6000: quit and restart it, it reads" >&2
    echo "the nolisten_tcp setting only when it starts." >&2
    exit 2
  fi
  cookie=$("$xauth" list :0 2>/dev/null | awk 'NR==1 {print $3}')
  if [ -z "$cookie" ]; then
    allow_bridge
  fi
}

case "${1:-}" in
  stop)
    if [ "$engine" = container ]; then
      container system stop >/dev/null 2>&1 || { echo "container system stop failed" >&2; exit 1; }
      echo "container service stopped"
    fi
    exit 0
    ;;
  xhost)
    allow_bridge
    DISPLAY=:0 "$xhost" | head -1
    exit 0
    ;;
  forward)
    exec python3 "$repo/scripts/gtk-container/fwd-host.py" "${FORWARD_PORT:-7777}" "${2:-$host_socket}"
    ;;
  "")
    echo "usage: $0 build | make ... | test-ui | shell | xhost | x11 [sample|dev|live] | forward [SOCKET] | stop | <command...>" >&2
    exit 2
    ;;
esac

mkdir -p "$cache/gomod" "$cache/gocache" "$cache/ccache"
if [ "$engine" = container ]; then
  container system start >/dev/null 2>&1 || true
fi
build_image() {
  "$engine" build -t "$image" -f "$repo/scripts/gtk-container/Containerfile" "$repo/scripts/gtk-container"
}
if [ "$1" = build ]; then
  build_image
  exit $?
fi
case "$engine" in
  container|docker|podman)
    if ! "$engine" image inspect "$image" >/dev/null 2>&1; then
      echo "gtk.sh: no image $image yet, building it first (a few minutes)" >&2
      build_image
    fi
    ;;
esac

build_dir=build/linux
x11_opts=()
case "$1" in
  make)
    shift
    set -- make "BUILD_DIR=$build_dir" "$@"
    ;;
  test-ui)
    set -- sh -c "make BUILD_DIR=$build_dir blueprint schemas && cd ui && GSETTINGS_SCHEMA_DIR=/src/$build_dir/glib-2.0/schemas GSETTINGS_BACKEND=memory xvfb-run -a -s \"-screen 0 1600x1200x24\" go test \$(pkg-config --exists gsound || echo -tags nosound) ./..."
    ;;
  shell)
    set -- bash
    ;;
  x11)
    mode=${2:-sample}
    case "$mode" in
      sample|dev|live) ;;
      *) echo "x11: unknown mode $mode (sample | dev | live)" >&2; exit 2 ;;
    esac
    if [ "$mode" = live ] && { [ ! -S "$host_socket" ] || [ ! -f "$host_socket.key" ]; }; then
      echo "x11 live: no daemon socket at $host_socket (or its .key). In your own Terminal:" >&2
      echo "  scripts/gtk-container/test-daemon.sh DATA_COPY $host_socket" >&2
      echo "and in another one:" >&2
      echo "  scripts/gtk-container/gtk.sh forward $host_socket" >&2
      exit 2
    fi
    x11_preflight
    # The Mac's zone and language, so that the client and the daemon show
    # local time and the user's language.
    tz=$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||')
    [ -n "$tz" ] || tz=UTC
    # The inner script: XDG dirs, the GSettings keyfile, the display, then
    # the app. dev keeps everything under /data (the host's
    # $cache/data) and starts gnome-keyring as the Secret Service of the
    # session bus before the app; the client starts malachid itself.
    set -- sh -c '
set -e
export XDG_RUNTIME_DIR=/tmp/mal
mkdir -p -m 700 /tmp/mal
if [ "$X11_MODE" = dev ]; then
  export XDG_CONFIG_HOME=/data/config XDG_DATA_HOME=/data/share XDG_CACHE_HOME=/data/cache
  mkdir -p /data/config/glib-2.0/settings /data/share /data/cache
else
  export XDG_CONFIG_HOME=/tmp/mal/config
  mkdir -p /tmp/mal/config/glib-2.0/settings
fi
keyfile=$XDG_CONFIG_HOME/glib-2.0/settings/keyfile
if [ "$X11_MODE" != dev ] || [ ! -f "$keyfile" ]; then
  printf "[io/github/schotek/Malachi]\nwindow-width=1280\nwindow-height=820\n" > "$keyfile"
fi
gw=$(python3 -c "import struct,socket
for l in open(\"/proc/net/route\").read().splitlines()[1:]:
    f=l.split()
    if f[1]==\"00000000\": print(socket.inet_ntoa(struct.pack(\"<L\", int(f[2],16)))); break")
# The Mac display'"'"'s cookie as the cookie of $gw:0; the app and the daemon
# it starts get XAUTHORITY, not the variable.
if [ -n "$XAUTH_COOKIE" ]; then
  export XAUTHORITY=/tmp/mal/Xauthority
  touch "$XAUTHORITY"
  chmod 600 "$XAUTHORITY"
  xauth -q add "$gw:0" MIT-MAGIC-COOKIE-1 "$XAUTH_COOKIE"
fi
unset XAUTH_COOKIE
extra="MALACHI_DAEMON=none MALACHI_BOARD_SAMPLES=1 MALACHI_CALENDAR_SAMPLES=1 MALACHI_DATA_DIR=/tmp/mal"
if [ "$X11_MODE" = live ]; then
  python3 /src/scripts/gtk-container/fwd-container.py /tmp/mal/rpc.sock "$gw" "$FORWARD_PORT" &
  sleep 1
  cp /hostsock/'"$(basename "$host_socket")"'.key /tmp/mal/rpc.sock.key && chmod 600 /tmp/mal/rpc.sock.key
  extra="MALACHI_DAEMON=none MALACHI_SOCKET=/tmp/mal/rpc.sock MALACHI_DATA_DIR=/tmp/mal"
elif [ "$X11_MODE" = dev ]; then
  extra=""
fi
set -- env DISPLAY=$gw:0 GDK_BACKEND=x11 GSK_RENDERER=cairo $extra GSETTINGS_SCHEMA_DIR=/src/'"$build_dir"'/glib-2.0/schemas GSETTINGS_BACKEND=keyfile MALACHI_LOCALE_DIR=/src/'"$build_dir"'/locale WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 MALACHI_LOG_LEVEL=info ./'"$build_dir"'/malachi
cd /src
if [ "$X11_MODE" = dev ]; then
  # gnome-keyring as org.freedesktop.secrets on the session bus. --unlock
  # reads the password from stdin, creates the login keyring in
  # $XDG_DATA_HOME/keyrings on the first run and unlocks it later. The
  # fixed password "malachi" is a development convenience: the keyring
  # file is only as safe as ~/.cache/malachi-gtk/data on the Mac.
  exec dbus-run-session -- sh -ec "eval \"\$(printf %s malachi | gnome-keyring-daemon --unlock --components=secrets)\"; export GNOME_KEYRING_CONTROL; exec \"\$@\"" keyring "$@"
fi
exec dbus-run-session -- "$@"'
    x11_opts=(-e "X11_MODE=$mode" -e "FORWARD_PORT=${FORWARD_PORT:-7777}" -e "TZ=$tz" -e "LANG=${LANG:-C.UTF-8}" -e "XAUTH_COOKIE=$cookie")
    if [ "$mode" = live ]; then
      x11_opts+=(-v "$(dirname "$host_socket"):/hostsock")
    elif [ "$mode" = dev ]; then
      mkdir -p "$cache/data"
      x11_opts+=(-v "$cache/data:/data")
    fi
    ;;
esac

# Every make inside the container writes to build/linux, never next to the
# macOS or Windows products in build/ (MAKEFLAGS carries the variable to
# a plain `make` run from a shell too).
opts=(--rm -w /src -e "MAKEFLAGS=BUILD_DIR=$build_dir" -v "$repo:/src" -v "$cache/gomod:/go/pkg/mod" -v "$cache/gocache:/gocache" -v "$cache/ccache:/ccache" ${x11_opts[@]+"${x11_opts[@]}"})
if [ "$engine" = container ]; then
  opts+=(--cpus "${CPUS:-8}" --memory "${MEMORY:-8G}")
fi
if [ -t 0 ]; then
  opts+=(-i -t)
fi
if [ -n "${PUBLISH:-}" ]; then
  opts+=(--publish "$PUBLISH:$PUBLISH")
fi
exec "$engine" run "${opts[@]}" "$image" "$@"
