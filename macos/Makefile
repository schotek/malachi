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
# keeps the identity stable across rebuilds. A release is signed with a
# Developer ID Application identity (SIGN='Developer ID Application: …'),
# and then every binary also gets the hardened runtime and a secure
# timestamp, which notarisation requires. Each binary carries a fixed
# identifier: without one codesign derives it from the linker's UUID, a
# new one per build, and the Keychain would ask again after every release
# even with Developer ID.
SIGN          ?= -
RELEASE_SIGN  := $(findstring Developer ID Application,$(SIGN))
CODESIGN      := codesign --force --sign "$(SIGN)"$(if $(RELEASE_SIGN), --options runtime --timestamp)
# Under the hardened runtime the app needs this to send Apple events.
ENTITLEMENTS  := Resources/MalachiMail.entitlements

# The architectures of the Swift binaries: empty is this Mac's own, the
# quick build for development. The root Makefile's macos-dmg builds arm64
# and x86_64, a universal bundle, and hands over malachid and malachi-mcp
# joined the same way in GO_DIR. Several architectures put SwiftPM's
# products in another directory, which --show-bin-path with the same flags
# names.
ARCHS         ?=
SWIFT_BUILD   := $(strip swift build -c release $(foreach a,$(ARCHS),--arch $(a)))
GO_DIR        ?= $(BUILD_DIR)

# The disk image a release carries, named after the architectures it runs on.
DMG_ARCH      := $(if $(word 2,$(ARCHS)),universal,$(or $(ARCHS),$(shell uname -m)))
DMG           := $(BUILD_DIR)/Malachi-Mail-$(VERSION)-$(DMG_ARCH).dmg

# Notarisation through notarytool: a keychain profile stored once with
# `xcrun notarytool store-credentials` (NOTARY_PROFILE), or an App Store
# Connect API key, as CI uses (NOTARY_KEY = the .p8 file, NOTARY_KEY_ID,
# NOTARY_ISSUER).
NOTARY_PROFILE ?=
NOTARY_KEY     ?=
NOTARY_KEY_ID  ?=
NOTARY_ISSUER  ?=
NOTARY_AUTH    := $(if $(NOTARY_PROFILE),--keychain-profile "$(NOTARY_PROFILE)",--key "$(NOTARY_KEY)" --key-id "$(NOTARY_KEY_ID)" --issuer "$(NOTARY_ISSUER)")

# The SDK the app records as linked against: AppKit picks the design by it
# (Liquid Glass from 26 on). SwiftPM's new build system (Swift 6.4) records
# the deployment target instead, so without this the app got the macOS 14
# look on any newer system. app writes the SDK into the bundle's copy of
# the executable with vtool, whatever built it: the linker flag that does
# the same (-platform_version) breaks SwiftPM's universal build in Xcode
# 26, which hands it to clang as clang's own arguments. MACOS_MIN is
# Package.swift's `platforms` minimum. Only the app gets it:
# malachi-keychain has no UI, and a changed binary is a new code identity
# the Keychain would ask about again for every stored password.
MACOS_MIN     := 14.0
SDK_VERSION   := $(shell xcrun --show-sdk-version 2>/dev/null)

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
# The application icon: the raster of the GTK icon rendered into an .icns
# with the tools macOS ships (sips, iconutil); no SVG rasteriser needed.
ICON_SRC      := $(ROOT)/docs/malachi_icon.png
ICONSET       := $(BUILD_DIR)/macos/Malachi.iconset
ICNS          := $(BUILD_DIR)/macos/Malachi.icns
LOCALIZATIONS := $(foreach l,$(LANGS),<string>$(l)</string>)

EXE           := MalachiMail
HELPER        := malachi-keychain
APP           := $(BUILD_DIR)/Malachi Mail.app
CONTENTS      := $(APP)/Contents

# set_sdk: shell statements for app's recipe that write SDK_VERSION into the
# executable $(1) and check that each of its architectures records it.
set_sdk = vtool -set-build-version macos $(MACOS_MIN) $(SDK_VERSION) -replace -output "$(1).sdk" "$(1)"; mv "$(1).sdk" "$(1)"; for arch in $$(lipo -archs "$(1)"); do vtool -arch "$$arch" -show-build "$(1)" | grep -Eq '^ *sdk $(SDK_VERSION)$$' || { echo "macos: $(1) does not record SDK $(SDK_VERSION) for $$arch" >&2; exit 1; }; done;

.PHONY: build locale icon app dmg notarize test run clean

## build: compile the Swift package (release) for ARCHS; a warning fails the build
build:
	$(SWIFT_BUILD) -Xswiftc -warnings-as-errors --product $(HELPER)
	$(SWIFT_BUILD) -Xswiftc -warnings-as-errors --product $(EXE)

## locale: generate .lproj directories from po/malachi.pot and po/*.po
locale:
	@mkdir -p "$(LOCALE_DIR)"
	python3 scripts/po2strings.py --pot "$(PO_DIR)/malachi.pot" --out "$(LOCALE_DIR)" $(PO_FILES)

## icon: render the .icns from docs/malachi_icon.png (every size the Dock and Finder use)
# The source keeps a transparent margin of about 65 px around its rounded
# square (1254 px canvas, square at 65,68–1190,1185); the Dock of macOS 26+
# shows that margin as a grey backing tile, so the square is cut out first
# and fills the icon edge to edge.
icon:
	@set -e; rm -rf "$(ICONSET)"; mkdir -p "$(ICONSET)"; \
	sips -c 1126 1126 --cropOffset 64 65 "$(ICON_SRC)" --out "$(ICONSET)/source.png" >/dev/null; \
	for s in 16 32 128 256 512; do \
		sips -z $$s $$s "$(ICONSET)/source.png" --out "$(ICONSET)/icon_$${s}x$${s}.png" >/dev/null; \
		d=$$((s * 2)); \
		sips -z $$d $$d "$(ICONSET)/source.png" --out "$(ICONSET)/icon_$${s}x$${s}@2x.png" >/dev/null; \
	done; \
	rm "$(ICONSET)/source.png"; \
	iconutil -c icns -o "$(ICNS)" "$(ICONSET)"

## app: assemble build/Malachi Mail.app with malachid, malachi-mcp and malachi-keychain inside
# The licences go into Contents/Resources, as into the Windows app folder
# (LICENSING.md). With ARCHS every executable is checked to run on each.
app: build locale icon
	@test -x "$(GO_DIR)/malachid" -a -x "$(GO_DIR)/malachi-mcp" || { \
		echo "macos: $(GO_DIR)/malachid or malachi-mcp is missing; run 'make backend mcp' in $(ROOT) first" >&2; exit 1; }
	@set -e; bin="$$($(SWIFT_BUILD) --show-bin-path)"; \
	rm -rf "$(APP)"; \
	mkdir -p "$(CONTENTS)/MacOS" "$(CONTENTS)/Resources"; \
	cp "$$bin/$(EXE)" "$(CONTENTS)/MacOS/$(EXE)"; \
	cp "$$bin/$(HELPER)" "$(CONTENTS)/MacOS/$(HELPER)"; \
	cp "$(GO_DIR)/malachid" "$(GO_DIR)/malachi-mcp" "$(CONTENTS)/MacOS/"; \
	$(if $(SDK_VERSION),$(call set_sdk,$(CONTENTS)/MacOS/$(EXE))) \
	for arch in $(ARCHS); do \
		for f in "$(CONTENTS)"/MacOS/*; do \
			lipo "$$f" -verify_arch "$$arch" || { echo "macos: $$f has no $$arch code" >&2; exit 1; }; \
		done; \
	done; \
	cp -R "$(LOCALE_DIR)"/*.lproj "$(CONTENTS)/Resources/"; \
	cp "$(ICNS)" "$(CONTENTS)/Resources/Malachi.icns"; \
	cp "$(ROOT)/LICENSE" "$(CONTENTS)/Resources/LICENSE.txt"; \
	cp "$(ROOT)/backend/LICENSE" "$(CONTENTS)/Resources/LICENSE-backend.txt"; \
	cp "$(ROOT)/LICENSING.md" "$(CONTENTS)/Resources/LICENSING.md"; \
	sed -e 's/@VERSION@/$(SHORT_VERSION)/g' -e 's/@FULL_VERSION@/$(VERSION)/g' \
	    -e 's/@BUNDLE_ID@/$(APP_ID)/g' -e 's/@EXECUTABLE@/$(EXE)/g' \
	    -e 's|@LOCALIZATIONS@|$(LOCALIZATIONS)|g' \
	    Resources/Info.plist.in > "$(CONTENTS)/Info.plist"; \
	plutil -lint "$(CONTENTS)/Info.plist" >/dev/null; \
	plutil -lint "$(ENTITLEMENTS)" >/dev/null; \
	printf 'APPL????' > "$(CONTENTS)/PkgInfo"; \
	for f in malachid malachi-mcp $(HELPER); do \
		$(CODESIGN) --identifier "$(APP_ID).$$f" "$(CONTENTS)/MacOS/$$f"; \
	done; \
	$(CODESIGN) --entitlements "$(ENTITLEMENTS)" "$(APP)"; \
	codesign --verify --strict --deep "$(APP)"; \
	echo "built $(APP) ($(VERSION), $(or $(ARCHS),$(shell uname -m)), languages: $(LANGS))"

## dmg: put the bundle into build/Malachi-Mail-<version>-<arch>.dmg beside a link to /Applications
# A Developer ID build signs the image as well. hdiutil on CI runners now
# and then fails with "Resource busy"; a second attempt goes through.
dmg: app
	@set -e; stage="$(BUILD_DIR)/macos/dmg"; \
	rm -rf "$$stage" "$(DMG)"; mkdir -p "$$stage"; \
	ditto "$(APP)" "$$stage/Malachi Mail.app"; \
	ln -s /Applications "$$stage/Applications"; \
	n=0; until hdiutil create -quiet -volname "Malachi Mail" -srcfolder "$$stage" -fs HFS+ -format UDZO -ov "$(DMG)"; do \
		n=$$((n + 1)); [ $$n -lt 3 ] || exit 1; sleep 5; \
	done; \
	rm -rf "$$stage"; \
	$(if $(RELEASE_SIGN),codesign --force --sign "$(SIGN)" --timestamp "$(DMG)";) \
	hdiutil verify -quiet "$(DMG)"; \
	echo "built $(DMG)"

## notarize: have Apple notarise the DMG, staple the ticket to it and check what Gatekeeper says (Developer ID only)
# notarytool exits non-zero for a rejected submission too; the verdict is
# read from its JSON either way, and a rejection prints Apple's log.
notarize:
	@test -n "$(RELEASE_SIGN)" || { echo "macos: notarize needs SIGN='Developer ID Application: …'" >&2; exit 1; }
	@test -f "$(DMG)" || { echo "macos: $(DMG) is missing; run make dmg with the same VERSION and ARCHS first" >&2; exit 1; }
	@set -e; out="$(BUILD_DIR)/macos/notary.json"; \
	xcrun notarytool submit "$(DMG)" $(NOTARY_AUTH) --wait --timeout 1h --output-format json > "$$out" || true; \
	id="$$(plutil -extract id raw -o - "$$out" 2>/dev/null || true)"; \
	status="$$(plutil -extract status raw -o - "$$out" 2>/dev/null || true)"; \
	echo "notarisation $${id:-(none)}: $${status:-no answer}"; \
	if [ "$$status" != Accepted ]; then \
		cat "$$out" >&2; \
		[ -z "$$id" ] || xcrun notarytool log "$$id" $(NOTARY_AUTH) >&2; \
		exit 1; \
	fi; \
	xcrun stapler staple "$(DMG)"; \
	xcrun stapler validate "$(DMG)"; \
	spctl --assess --type open --context context:primary-signature --verbose=2 "$(DMG)"; \
	spctl --assess --type execute --verbose=2 "$(APP)"

## test: run the Swift tests (with the generated catalogues, so the Czech cases run) and the script tests
test: locale
	python3 -m unittest discover -s scripts -p 'test_*.py'
	python3 scripts/check-strings.py
	MALACHI_LOCALE_DIR="$(LOCALE_DIR)" swift test

## run: run the bundled executable from the terminal (logs of the app and of malachid stay visible)
run: app
	"$(CONTENTS)/MacOS/$(EXE)"

## clean: remove the SwiftPM scratch directory, the catalogues, the bundle and the disk images
clean:
	rm -rf .build "$(LOCALE_DIR)" "$(ICONSET)" "$(ICNS)" "$(APP)" "$(BUILD_DIR)"/Malachi-Mail-*.dmg
