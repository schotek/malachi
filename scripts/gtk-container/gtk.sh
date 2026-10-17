#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Build and test the GTK client inside the malachi-gtk:f42 container
# (scripts/gtk-container/Containerfile) over this checkout, with the Go module
# cache, the Go build cache and ccache kept on the host, so that gotk4 is
# compiled once. Meant for machines without GTK (a Mac with Apple's
# `container` CLI); the Toolbx stays the primary environment.
#
#   scripts/gtk-container/gtk.sh build            build the image (once)
#   scripts/gtk-container/gtk.sh make build       any command runs in /src
#   scripts/gtk-container/gtk.sh test-ui          GTK UI tests under Xvfb
#   scripts/gtk-container/gtk.sh shell            an interactive shell
#   scripts/gtk-container/gtk.sh xhost            allow the container bridge on XQuartz
#   scripts/gtk-container/gtk.sh x11              the app in an XQuartz window over sample data
#   scripts/gtk-container/gtk.sh x11 live         … over a daemon on the Mac (see forward)
#   scripts/gtk-container/gtk.sh forward [SOCKET] host side of `x11 live`: forwards the
#                                                 daemon's unix socket to the container
#
# XQuartz: install it, `defaults write org.xquartz.X11 nolisten_tcp -bool
# false`, restart XQuartz (it then listens on TCP 6000), `gtk.sh xhost`.
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
# With Apple's container, stop the service afterwards: container system stop.
set -eu
repo=$(cd "$(dirname "$0")/../.." && pwd)
engine=${ENGINE:-container}
image=malachi-gtk:f42
cache=${MALACHI_GTK_CACHE:-$HOME/.cache/malachi-gtk}
mkdir -p "$cache/gomod" "$cache/gocache" "$cache/ccache"

if [ "$engine" = container ]; then
  container system start >/dev/null 2>&1 || true
fi

build_dir=build/linux
x11_opts=()
case "${1:-}" in
  build)
    exec "$engine" build -t "$image" -f "$repo/scripts/gtk-container/Containerfile" "$repo/scripts/gtk-container"
    ;;
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
  xhost)
    # XQuartz's local display; the bridge is 192.168.64.0/24 (xhost takes
    # single addresses).
    for i in $(seq 2 254); do DISPLAY=:0 /opt/X11/bin/xhost "+192.168.64.$i" >/dev/null 2>&1; done
    DISPLAY=:0 /opt/X11/bin/xhost | head -1
    exit 0
    ;;
  forward)
    exec python3 "$repo/scripts/gtk-container/fwd-host.py" "${FORWARD_PORT:-7777}" "${2:-/tmp/malachi-gtk/rpc.sock}"
    ;;
  x11)
    mode=${2:-sample}
    x11_live=0
    [ "$mode" = live ] && x11_live=1
    socket_dir=$(dirname "${HOST_SOCKET:-/tmp/malachi-gtk/rpc.sock}")
    set -- sh -c '
set -e
export XDG_RUNTIME_DIR=/tmp/mal XDG_CONFIG_HOME=/tmp/mal/config
mkdir -p /tmp/mal/config/glib-2.0/settings
printf "[io/github/schotek/Malachi]\nwindow-width=1280\nwindow-height=820\n" > /tmp/mal/config/glib-2.0/settings/keyfile
gw=$(python3 -c "import struct,socket
for l in open(\"/proc/net/route\").read().splitlines()[1:]:
    f=l.split()
    if f[1]==\"00000000\": print(socket.inet_ntoa(struct.pack(\"<L\", int(f[2],16)))); break")
extra="MALACHI_DAEMON=none MALACHI_BOARD_SAMPLES=1 MALACHI_CALENDAR_SAMPLES=1"
if [ "$X11_LIVE" = 1 ]; then
  python3 /src/scripts/gtk-container/fwd-container.py /tmp/mal/rpc.sock "$gw" "$FORWARD_PORT" &
  sleep 1
  cp /hostsock/'"$(basename "${HOST_SOCKET:-/tmp/malachi-gtk/rpc.sock}")"'.key /tmp/mal/rpc.sock.key && chmod 600 /tmp/mal/rpc.sock.key
  extra="MALACHI_DAEMON=none MALACHI_SOCKET=/tmp/mal/rpc.sock"
fi
cd /src && exec dbus-run-session -- env DISPLAY=$gw:0 GDK_BACKEND=x11 GSK_RENDERER=cairo $extra MALACHI_DATA_DIR=/tmp/mal GSETTINGS_SCHEMA_DIR=/src/'"$build_dir"'/glib-2.0/schemas GSETTINGS_BACKEND=keyfile MALACHI_LOCALE_DIR=/src/'"$build_dir"'/locale WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 MALACHI_LOG_LEVEL=info ./'"$build_dir"'/malachi'
    x11_opts=(-e "X11_LIVE=$x11_live" -e "FORWARD_PORT=${FORWARD_PORT:-7777}" -v "$socket_dir:/hostsock")
    ;;
  "")
    echo "usage: $0 build | test-ui | shell | <command...>" >&2
    exit 2
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
