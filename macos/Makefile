# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Malachi Mail — the macOS app bundle. Driven by the root Makefile
# (`make macos`, `make run-macos`, `make test-macos`), which passes BUILD_DIR,
# VERSION and APP_ID; runs on its own with the defaults below for quick
# iteration from this directory.

ROOT          := $(abspath ..)
BUILD_DIR     ?= $(ROOT)/build
APP_ID        ?= io.github.schotek.Malachi
VERSION       ?= $(shell { git describe --tags --always --dirty 2>/dev/null || echo dev; } | sed 's/^v//')
# CFBundleShortVersionString and CFBundleVersion allow one to three integers:
# 0.1.0-33-gabc1234-dirty → 0.1.0; a bare commit hash (no tag yet) → 0.0.0.
# The full string is kept in the custom Info.plist key MalachiVersion.
SHORT_VERSION := $(shell printf '%s' '$(VERSION)' | sed -nE 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/p')
ifeq ($(SHORT_VERSION),)
SHORT_VERSION := 0.0.0
endif

# Code-signing identity. "-" is ad hoc: enough for the machine the bundle was
# built on, but every rebuild is a new identity, so the Keychain asks again
# whether malachi-keychain may read the stored passwords. A self-signed
# code-signing certificate from Keychain Access (make macos SIGN='Malachi Dev')
# keeps the identity stable across rebuilds.
SIGN          ?= -

# Localisation: the GTK catalogue in po/ is the single source of truth; the
# generated .lproj directories are copied into Contents/Resources and read by
# MalachiCore's L10n (never Bundle.module, which a hand-made .app cannot carry).
PO_DIR        := $(ROOT)/po
# The languages come from po/LINGUAS, as in the root Makefile, so both
# clients ship the same set.
LINGUAS       := $(shell grep -v '^\#' $(PO_DIR)/LINGUAS 2>/dev/null)
PO_FILES      := $(foreach l,$(LINGUAS),$(PO_DIR)/$(l).po)
LANGS         := en $(LINGUAS)
LOCALE_DIR    := $(BUILD_DIR)/macos/locale
LOCALIZATIONS := $(foreach l,$(LANGS),<string>$(l)</string>)

EXE           := MalachiMail
HELPER        := malachi-keychain
APP           := $(BUILD_DIR)/Malachi Mail.app
CONTENTS      := $(APP)/Contents

.PHONY: build locale app test run clean

## build: compile the Swift package (release); a warning fails the build
build:
	swift build -c release -Xswiftc -warnings-as-errors

## locale: generate .lproj directories from po/malachi.pot and po/*.po
locale:
	@mkdir -p "$(LOCALE_DIR)"
	python3 scripts/po2strings.py --pot "$(PO_DIR)/malachi.pot" --out "$(LOCALE_DIR)" $(PO_FILES)

## app: assemble build/Malachi Mail.app with malachid, malachi-mcp and malachi-keychain inside
app: build locale
	@test -x "$(BUILD_DIR)/malachid" -a -x "$(BUILD_DIR)/malachi-mcp" || { \
		echo "macos: $(BUILD_DIR)/malachid or malachi-mcp is missing; run 'make backend mcp' in $(ROOT) first" >&2; exit 1; }
	@set -e; bin="$$(swift build -c release --show-bin-path)"; \
	rm -rf "$(APP)"; \
	mkdir -p "$(CONTENTS)/MacOS" "$(CONTENTS)/Resources"; \
	cp "$$bin/$(EXE)" "$(CONTENTS)/MacOS/$(EXE)"; \
	cp "$$bin/$(HELPER)" "$(CONTENTS)/MacOS/$(HELPER)"; \
	cp "$(BUILD_DIR)/malachid" "$(BUILD_DIR)/malachi-mcp" "$(CONTENTS)/MacOS/"; \
	cp -R "$(LOCALE_DIR)"/*.lproj "$(CONTENTS)/Resources/"; \
	sed -e 's/@VERSION@/$(SHORT_VERSION)/g' -e 's/@FULL_VERSION@/$(VERSION)/g' \
	    -e 's/@BUNDLE_ID@/$(APP_ID)/g' -e 's/@EXECUTABLE@/$(EXE)/g' \
	    -e 's|@LOCALIZATIONS@|$(LOCALIZATIONS)|g' \
	    Resources/Info.plist.in > "$(CONTENTS)/Info.plist"; \
	plutil -lint "$(CONTENTS)/Info.plist" >/dev/null; \
	printf 'APPL????' > "$(CONTENTS)/PkgInfo"; \
	codesign --force --sign "$(SIGN)" "$(CONTENTS)/MacOS/malachid"; \
	codesign --force --sign "$(SIGN)" "$(CONTENTS)/MacOS/malachi-mcp"; \
	codesign --force --sign "$(SIGN)" "$(CONTENTS)/MacOS/$(HELPER)"; \
	codesign --force --sign "$(SIGN)" "$(APP)"; \
	echo "built $(APP) ($(VERSION), languages: $(LANGS))"

## test: run the Swift tests (with the generated catalogues, so the Czech cases run) and the script tests
test: locale
	python3 -m unittest discover -s scripts -p 'test_*.py'
	python3 scripts/check-strings.py
	MALACHI_LOCALE_DIR="$(LOCALE_DIR)" swift test

## run: run the bundled executable from the terminal (logs of the app and of malachid stay visible)
run: app
	"$(CONTENTS)/MacOS/$(EXE)"

## clean: remove the SwiftPM scratch directory, the catalogues and the bundle
clean:
	rm -rf .build "$(LOCALE_DIR)" "$(APP)"
