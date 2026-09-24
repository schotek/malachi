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

EXE           := MalachiMail
APP           := $(BUILD_DIR)/Malachi Mail.app
CONTENTS      := $(APP)/Contents

.PHONY: build app test run clean

## build: compile the Swift package (release)
build:
	swift build -c release

## app: assemble build/Malachi Mail.app with malachid and malachi-mcp inside
app: build
	@test -x "$(BUILD_DIR)/malachid" -a -x "$(BUILD_DIR)/malachi-mcp" || { \
		echo "macos: $(BUILD_DIR)/malachid or malachi-mcp is missing; run 'make backend mcp' in $(ROOT) first" >&2; exit 1; }
	@set -e; bin="$$(swift build -c release --show-bin-path)"; \
	rm -rf "$(APP)"; \
	mkdir -p "$(CONTENTS)/MacOS" "$(CONTENTS)/Resources"; \
	cp "$$bin/$(EXE)" "$(CONTENTS)/MacOS/$(EXE)"; \
	cp "$(BUILD_DIR)/malachid" "$(BUILD_DIR)/malachi-mcp" "$(CONTENTS)/MacOS/"; \
	sed -e 's/@VERSION@/$(SHORT_VERSION)/g' -e 's/@FULL_VERSION@/$(VERSION)/g' \
	    -e 's/@BUNDLE_ID@/$(APP_ID)/g' -e 's/@EXECUTABLE@/$(EXE)/g' \
	    Resources/Info.plist.in > "$(CONTENTS)/Info.plist"; \
	printf 'APPL????' > "$(CONTENTS)/PkgInfo"; \
	codesign --force --sign - "$(CONTENTS)/MacOS/malachid"; \
	codesign --force --sign - "$(CONTENTS)/MacOS/malachi-mcp"; \
	codesign --force --sign - "$(APP)"; \
	echo "built $(APP) ($(VERSION))"

## test: run the Swift tests
test:
	swift test

## run: run the bundled executable from the terminal (logs of the app and of malachid stay visible)
run: app
	"$(CONTENTS)/MacOS/$(EXE)"

## clean: remove the SwiftPM scratch directory and the bundle
clean:
	rm -rf .build "$(APP)"
