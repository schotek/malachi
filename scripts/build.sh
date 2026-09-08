#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Full build: Blueprint → Go backend → Go UI → rendered data files.
# Thin wrapper around the Makefile so CI, the Flatpak manifest and the
# Debian package have one entry point. Pass PREFIX to also install.
#
# Usage: scripts/build.sh            build into ./build
#        scripts/build.sh install    build and install into $PREFIX (default /app)
#
# PREFIX is compiled into the UI (the locale directory), DESTDIR only moves
# the files: a distribution package stages into DESTDIR with PREFIX=/usr, so
# the installed binary still looks in /usr/share/locale.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APP_ID="io.github.schotek.Malachi"
PREFIX="${PREFIX:-/app}"
DESTDIR="${DESTDIR:-}"

make PREFIX="$PREFIX" build data

if [[ "${1:-}" == "install" ]]; then
    while read -r lang; do
        [[ -z "$lang" || "$lang" == \#* ]] && continue
        install -Dm644 "build/locale/$lang/LC_MESSAGES/malachi.mo" \
            "$DESTDIR$PREFIX/share/locale/$lang/LC_MESSAGES/malachi.mo"
    done < po/LINGUAS
    install -Dm755 build/malachid "$DESTDIR$PREFIX/bin/malachid"
    install -Dm755 build/malachi  "$DESTDIR$PREFIX/bin/malachi"
    install -Dm644 "data/$APP_ID.desktop" \
        "$DESTDIR$PREFIX/share/applications/$APP_ID.desktop"
    # DBusActivatable=true in the desktop file; flatpak build-export rejects
    # the build when the matching service file is not exported.
    install -Dm644 "data/$APP_ID.service" \
        "$DESTDIR$PREFIX/share/dbus-1/services/$APP_ID.service"
    install -Dm644 "data/$APP_ID.metainfo.xml" \
        "$DESTDIR$PREFIX/share/metainfo/$APP_ID.metainfo.xml"
    install -Dm644 "data/$APP_ID.gschema.xml" \
        "$DESTDIR$PREFIX/share/glib-2.0/schemas/$APP_ID.gschema.xml"
    # Not conditional: appstreamcli compose (and so the whole Flatpak build)
    # fails with icon-not-found when the application icon is missing, and a
    # silent skip here is what hid that until CI ran.
    install -Dm644 "ui/data/icons/$APP_ID.svg" \
        "$DESTDIR$PREFIX/share/icons/hicolor/scalable/apps/$APP_ID.svg"
    if [[ -f "ui/data/icons/$APP_ID-symbolic.svg" ]]; then
        install -Dm644 "ui/data/icons/$APP_ID-symbolic.svg" \
            "$DESTDIR$PREFIX/share/icons/hicolor/symbolic/apps/$APP_ID-symbolic.svg"
    fi
    # Staging for a package: the schema is compiled by the package manager
    # (dpkg triggers on /usr/share/glib-2.0/schemas), and a compiled cache
    # inside the package would collide with every other application's.
    if [[ -z "$DESTDIR" ]]; then
        glib-compile-schemas "$PREFIX/share/glib-2.0/schemas" || true
    fi
fi
