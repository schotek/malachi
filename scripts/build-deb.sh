#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Build a binary .deb for the architecture of the machine it runs on.
#
# This is a native package in the plain sense: it links the distribution's
# GTK 4, libadwaita and WebKitGTK rather than carrying a runtime, so it only
# runs on a release whose libadwaita is new enough (>= 1.7, the version that
# introduced AdwToggleGroup — Ubuntu 26.04 LTS and Debian 14, not Ubuntu
# 24.04 LTS, whose libadwaita is 1.5). The Flatpak stays the portable build.
#
# Run it on the target release (CI: .github/workflows/deb.yml), with the
# build dependencies of README "Building from source" installed plus dpkg-dev:
#   ./scripts/build-deb.sh          → build/malachi_<version>_<arch>.deb
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PKG="malachi"
TEMPLATE="packaging/debian/control.in"
STAGE="$ROOT/build/deb"

for tool in dpkg-deb dpkg-shlibdeps dpkg-architecture; do
    if ! command -v "$tool" >/dev/null; then
        echo "$tool is missing; install dpkg-dev" >&2
        exit 1
    fi
done

# The version dpkg sees. `git describe` between tags produces
# "0.1.0-21-g4f543f4", where dpkg would read everything after the last
# hyphen as the Debian revision; the hyphens therefore become "+", which
# also sorts a development build after the tag it follows.
VERSION="$(cat .version 2>/dev/null || git describe --tags --always --dirty | sed 's/^v//')"
VERSION="${VERSION//-/+}-1"
ARCH="$(dpkg-architecture -qDEB_HOST_ARCH)"

rm -rf "$STAGE"
PREFIX=/usr DESTDIR="$STAGE" ./scripts/build.sh install

# dpkg-shlibdeps reads the ELF files and names the packages that own the
# libraries they actually link, with the minimal version each symbol needs.
# It insists on a debian/control beside the tree it inspects, so a throwaway
# one exists for the length of the call and is never shipped.
mkdir -p "$STAGE/debian"
printf 'Source: %s\n\nPackage: %s\nArchitecture: %s\n' "$PKG" "$PKG" "$ARCH" > "$STAGE/debian/control"
DEPENDS="$(cd "$STAGE" && dpkg-shlibdeps -O --warnings=1 usr/bin/malachi usr/bin/malachid |
    sed 's/^shlibs:Depends=//')"
rm -rf "$STAGE/debian"

# What no ELF header can say: the GSettings backend the preferences need,
# and the libadwaita floor. Symbol versioning would give the floor only if
# the shared library carried a symbols file for it, so it is stated here.
DEPENDS="$DEPENDS, libadwaita-1-0 (>= 1.7), dconf-gsettings-backend | gsettings-backend"

SIZE="$(du -ks "$STAGE" | cut -f1)"

mkdir -p "$STAGE/DEBIAN"
sed -e "s|@VERSION@|$VERSION|" -e "s|@ARCH@|$ARCH|" -e "s|@SIZE@|$SIZE|" \
    -e "s|@DEPENDS@|$DEPENDS|" "$TEMPLATE" > "$STAGE/DEBIAN/control"

# md5sums lets dpkg --verify and debsums check an installed copy.
(cd "$STAGE" && find usr -type f -exec md5sum {} + > DEBIAN/md5sums)

# The staging directory is created with the umask of whoever runs this; the
# package must not carry that. Everything is world-readable, nothing
# group-writable, and dpkg-deb records root:root ownership.
chmod -R u+rwX,go+rX,go-w "$STAGE"

OUT="build/${PKG}_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group --build "$STAGE" "$OUT"
dpkg-deb --info "$OUT"
dpkg-deb --contents "$OUT"
echo "built $OUT"
