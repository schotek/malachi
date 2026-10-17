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
  "")
    echo "usage: $0 build | test-ui | shell | <command...>" >&2
    exit 2
    ;;
esac

opts=(--rm -w /src -v "$repo:/src" -v "$cache/gomod:/go/pkg/mod" -v "$cache/gocache:/gocache" -v "$cache/ccache:/ccache")
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
