#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Full build: Blueprint → Go backend → Go UI → rendered data files.
# Thin wrapper around the Makefile so CI and the Flatpak manifest have one
# entry point. Pass PREFIX to also install (used by the Flatpak build).
#
# Usage: scripts/build.sh            build into ./build
#        scripts/build.sh install    build and install into $PREFIX (default /app)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APP_ID="io.github.schotek.Malachi"
PREFIX="${PREFIX:-/app}"

make PREFIX="$PREFIX" build data

if [[ "${1:-}" == "install" ]]; then
    while read -r lang; do
        [[ -z "$lang" || "$lang" == \#* ]] && continue
        install -Dm644 "build/locale/$lang/LC_MESSAGES/malachi.mo" \
            "$PREFIX/share/locale/$lang/LC_MESSAGES/malachi.mo"
    done < po/LINGUAS
    install -Dm755 build/malachid "$PREFIX/bin/malachid"
    install -Dm755 build/malachi  "$PREFIX/bin/malachi"
    install -Dm644 "data/$APP_ID.desktop" \
        "$PREFIX/share/applications/$APP_ID.desktop"
    install -Dm644 "data/$APP_ID.metainfo.xml" \
        "$PREFIX/share/metainfo/$APP_ID.metainfo.xml"
    install -Dm644 "data/$APP_ID.gschema.xml" \
        "$PREFIX/share/glib-2.0/schemas/$APP_ID.gschema.xml"
    if [[ -f "ui/data/icons/$APP_ID.svg" ]]; then
        install -Dm644 "ui/data/icons/$APP_ID.svg" \
            "$PREFIX/share/icons/hicolor/scalable/apps/$APP_ID.svg"
    fi
    if [[ -f "ui/data/icons/$APP_ID-symbolic.svg" ]]; then
        install -Dm644 "ui/data/icons/$APP_ID-symbolic.svg" \
            "$PREFIX/share/icons/hicolor/symbolic/apps/$APP_ID-symbolic.svg"
    fi
    glib-compile-schemas "$PREFIX/share/glib-2.0/schemas" || true
fi
