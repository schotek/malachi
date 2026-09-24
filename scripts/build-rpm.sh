#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Build a binary .rpm for the architecture of the machine it runs on.
#
# The counterpart of scripts/build-deb.sh for Fedora and its relatives: a
# native package that links the distribution's GTK 4, libadwaita and
# WebKitGTK rather than carrying a runtime. Fedora 42 is the floor, for the
# same reason as Ubuntu 26.04 on the Debian side — libadwaita 1.7 and its
# AdwToggleGroup, which the message-list filter uses. The Flatpak stays the
# portable build.
#
# Run it on the target release (CI: .github/workflows/rpm.yml), with the
# build dependencies of README "Building from source" installed plus
# rpm-build:
#   ./scripts/build-rpm.sh          → build/malachi-<version>-1.<arch>.rpm
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PKG="malachi"
TEMPLATE="packaging/rpm/malachi.spec.in"
STAGE="$ROOT/build/rpm/stage"
TOPDIR="$ROOT/build/rpm/topdir"

for tool in rpmbuild rpm; do
    if ! command -v "$tool" >/dev/null; then
        echo "$tool is missing; install rpm-build" >&2
        exit 1
    fi
done

# The version rpm sees. `git describe` between tags produces
# "0.1.0-34-g0fa6575", and rpm reads a hyphen as the separator before the
# release. The hyphens therefore become "^", which rpm sorts *after* the
# release it follows — the tilde would sort before it and make a development
# build look older than the tag it came from.
VERSION="$(cat .version 2>/dev/null || git describe --tags --always --dirty | sed 's/^v//')"
VERSION="${VERSION//-/^}"
ARCH="$(rpm --eval '%{_arch}')"

rm -rf "$STAGE" "$TOPDIR"
PREFIX=/usr DESTDIR="$STAGE" ./scripts/build.sh install

# The schema cache belongs to the package manager, not to the package: RPM
# has a file trigger on %{_datadir}/glib-2.0/schemas that compiles it.
rm -f "$STAGE/usr/share/glib-2.0/schemas/gschemas.compiled"

mkdir -p "$TOPDIR"/{BUILD,RPMS,SPECS}

# Every staged file, as an absolute path, for %files. Directories are left
# out on purpose: /usr/bin, the icon theme and the locale tree all belong to
# other packages, and claiming them here would make this package own paths it
# only contributes to.
#
# Translations are marked %lang(<code>) so that a system configured with
# %_install_langs installs only the ones it wants; without the mark rpm has
# no way to tell a catalogue from an ordinary file.
FILELIST="$TOPDIR/filelist"
(cd "$STAGE" && find . \( -type f -o -type l \) -printf '/%P\n' | sort |
    awk -F/ '$0 ~ "^/usr/share/locale/" { print "%lang(" $5 ") " $0; next } { print }'
) > "$FILELIST"
test -s "$FILELIST"

# Filled in with bash substitution rather than sed: the staging path and the
# version are arbitrary text, and picking a delimiter that none of them can
# contain is a bet rather than a guarantee (see scripts/build-deb.sh).
SPEC="$(cat "$TEMPLATE")"
SPEC="${SPEC//@VERSION@/$VERSION}"
SPEC="${SPEC//@ARCH@/$ARCH}"
SPEC="${SPEC//@STAGE@/$STAGE}"
SPEC="${SPEC//@FILELIST@/$FILELIST}"
# rpm parses the changelog date itself and only accepts the C form of it.
SPEC="${SPEC//@DATE@/$(LC_ALL=C date '+%a %b %d %Y')}"
printf '%s\n' "$SPEC" > "$TOPDIR/SPECS/$PKG.spec"

rpmbuild -bb \
    --define "_topdir $TOPDIR" \
    --define "_rpmdir $ROOT/build" \
    --define "_rpmfilename %%{NAME}-%%{VERSION}-%%{RELEASE}.%%{ARCH}.rpm" \
    "$TOPDIR/SPECS/$PKG.spec"

OUT="$(ls -1 "$ROOT/build/$PKG"-*.rpm | tail -1)"
rpm -qip "$OUT"
rpm -qlp "$OUT"
rpm -qRp "$OUT"
echo "built $OUT"
