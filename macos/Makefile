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
# VARIANT=dev is a test build beside the stable Malachi Mail installed on
# the same Mac (the root Makefile's macos and run-macos): bundle
# identifier APP_ID.Dev and the name Malachi Mail Dev,
# so its preferences, notifications, login item and mailto: are its own,
# while the data directory, the daemon's socket and the keychain items,
# which go by name, are the stable app's. Info.plist names the stable app
# (MalachiStableBundleIdentifier), and MalachiCore's TestBuild then reads
# the stable app's preferences under its own, leaves the bridge's
# registration in Claude to it and uses only a daemon of its own version.
# release (the default) is every other build: the DMG, the App Store, CI.
VARIANT       ?= release
ifeq ($(VARIANT),dev)
BUNDLE_ID     := $(APP_ID).Dev
BUNDLE_NAME   := Malachi Mail Dev
STABLE_ID     := $(APP_ID)
else ifeq ($(VARIANT),release)
BUNDLE_ID     := $(APP_ID)
BUNDLE_NAME   := Malachi Mail
STABLE_ID     :=
else
$(error VARIANT must be release or dev, not $(VARIANT))
endif
VERSION       ?= $(shell { git describe --tags --always --dirty 2>/dev/null || echo dev; } | sed 's/^v//')
# CFBundleShortVersionString and CFBundleVersion allow one to three integers:
# 0.1.0-33-gabc1234-dirty → 0.1.0; a bare commit hash (no tag yet) → 0.0.0.
# The full string is kept in the custom Info.plist key MalachiVersion.
SHORT_VERSION := $(shell printf '%s' '$(VERSION)' | sed -nE 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/p')
ifeq ($(SHORT_VERSION),)
SHORT_VERSION := 0.0.0
endif
# CFBundleVersion. The Mac App Store wants it to grow with every upload, so
# its workflow passes the run's number (BUILD_NUMBER=0.4.0.17 is not valid:
# one to three integers); everything else keeps SHORT_VERSION.
BUILD_NUMBER  ?= $(SHORT_VERSION)

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
# And malachi-mcp this, for the native code wazero makes of the PDFium it
# embeds (the file says why). The bridge alone: never the app, malachid or
# malachi-keychain. Signed into every build, with or without the hardened
# runtime, so that an ad-hoc build carries what a release does.
MCP_ENTITLEMENTS := Resources/malachi-mcp.entitlements
# No entitlements for malachid and malachi-keychain in the DMG.
HELPER_ENTITLEMENTS :=

# DISTRIBUTION=appstore builds the Mac App Store variant from the same code
# (docs/plans/mac-app-store.md): Info.plist says so to MalachiCore's
# Distribution, every binary is signed with the App Sandbox from
# Resources/mas (malachid and malachi-keychain inheriting the app's), and
# the provisioning profile PROFILE, which the app's application identifier
# and app group need, goes into the bundle. SIGN is then an Apple
# Development identity for a local test or Apple Distribution for the
# store; neither gets the hardened runtime, which the App Store does not
# ask for.
DISTRIBUTION  ?= direct
PROFILE       ?=
# The App Store build carries malachi-mcp in a bundle of its own
# (MCP_APP), with the profile MCP_PROFILE of io.github.schotek.Malachi.malachi-mcp:
# Claude starts it, so it has a sandbox of its own, and a sandboxed
# executable outside a bundle gets neither the app group nor a container.
# Beside it malachi-mcp-worker, the same binary signed to inherit its
# sandbox: the bridge's document worker is a child, and a child that asks
# for a sandbox of its own is ended at start (extract_runner.go).
MCP_PROFILE   ?=
MCP_APP       = $(CONTENTS)/Helpers/Malachi Mail MCP.app
ifeq ($(DISTRIBUTION),appstore)
ENTITLEMENTS        := Resources/mas/MalachiMail.entitlements
MCP_ENTITLEMENTS    := Resources/mas/malachi-mcp.entitlements
HELPER_ENTITLEMENTS := Resources/mas/inherit.entitlements
else ifneq ($(DISTRIBUTION),direct)
$(error DISTRIBUTION must be direct or appstore, not $(DISTRIBUTION))
endif
ifeq ($(VARIANT)$(DISTRIBUTION),devappstore)
$(error VARIANT=dev is a direct build; the App Store build is always release)
endif

# The architectures of the Swift binaries: empty is this Mac's own, the
# quick build for development. The root Makefile's macos-dmg builds arm64
# and x86_64, a universal bundle, and hands over malachid and malachi-mcp
# joined the same way in GO_DIR. Several architectures put SwiftPM's
# products in another directory, which --show-bin-path with the same flags
# names.
ARCHS         ?=
SWIFT_BUILD   := $(strip swift build -c release $(foreach a,$(ARCHS),--arch $(a)))
GO_DIR        ?= $(BUILD_DIR)

# The disk image a release carries, named after the architectures it runs on
# as every release file is (docs/releasing.md §1): x86_64 is x64 there.
DMG_ARCH      := $(subst x86_64,x64,$(if $(word 2,$(ARCHS)),universal,$(or $(ARCHS),$(shell uname -m))))
DMG           := $(BUILD_DIR)/Malachi-Mail-$(VERSION)-macos-$(DMG_ARCH).dmg

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
ICON_SRC      := $(ROOT)/data/icons/malachi_icon.png
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

.PHONY: build locale icon app dmg notarize test run clean pkg validate-pkg

## build: compile the Swift package (release) for ARCHS; a warning fails the build
build:
	$(SWIFT_BUILD) -Xswiftc -warnings-as-errors --product $(HELPER)
	$(SWIFT_BUILD) -Xswiftc -warnings-as-errors --product $(EXE)

## locale: generate .lproj directories from po/malachi.pot and po/*.po
locale:
	@mkdir -p "$(LOCALE_DIR)"
	python3 scripts/po2strings.py --pot "$(PO_DIR)/malachi.pot" --out "$(LOCALE_DIR)" $(PO_FILES)

## icon: render the .icns from data/icons/malachi_icon.png (every size the Dock and Finder use)
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
# The licences and THIRD-PARTY-NOTICES.md go into Contents/Resources, as
# into the Windows app folder (LICENSING.md). With ARCHS every executable
# is checked to run on each.
app: build locale icon
	@test -x "$(GO_DIR)/malachid" -a -x "$(GO_DIR)/malachi-mcp" || { \
		echo "macos: $(GO_DIR)/malachid or malachi-mcp is missing; run 'make backend mcp' in $(ROOT) first" >&2; exit 1; }
	$(if $(filter appstore,$(DISTRIBUTION)),@test -f "$(PROFILE)" -a -f "$(MCP_PROFILE)" || { \
		echo "macos: DISTRIBUTION=appstore needs PROFILE=<the .provisionprofile of io.github.schotek.Malachi> and MCP_PROFILE=<that of io.github.schotek.Malachi.malachi-mcp>" >&2; exit 1; })
	@set -e; bin="$$($(SWIFT_BUILD) --show-bin-path)"; \
	rm -rf "$(APP)"; \
	mkdir -p "$(CONTENTS)/MacOS" "$(CONTENTS)/Resources"; \
	cp "$$bin/$(EXE)" "$(CONTENTS)/MacOS/$(EXE)"; \
	cp "$$bin/$(HELPER)" "$(CONTENTS)/MacOS/$(HELPER)"; \
	cp "$(GO_DIR)/malachid" "$(CONTENTS)/MacOS/"; \
	$(if $(filter appstore,$(DISTRIBUTION)),mkdir -p "$(MCP_APP)/Contents/MacOS"; \
	cp "$(GO_DIR)/malachi-mcp" "$(MCP_APP)/Contents/MacOS/"; \
	cp "$(GO_DIR)/malachi-mcp" "$(MCP_APP)/Contents/MacOS/malachi-mcp-worker"; \
	cp "$(MCP_PROFILE)" "$(MCP_APP)/Contents/embedded.provisionprofile"; \
	mkdir -p "$(MCP_APP)/Contents/Resources"; \
	cp Resources/mas/PrivacyInfo.xcprivacy "$(MCP_APP)/Contents/Resources/"; \
	sed -e 's/@VERSION@/$(SHORT_VERSION)/g' -e 's/@BUILD@/$(BUILD_NUMBER)/g' -e 's/@BUNDLE_ID@/$(APP_ID)/g' Resources/mas/mcp-Info.plist.in > "$(MCP_APP)/Contents/Info.plist"; \
	plutil -lint "$(MCP_APP)/Contents/Info.plist" >/dev/null;,cp "$(GO_DIR)/malachi-mcp" "$(CONTENTS)/MacOS/";) \
	$(if $(SDK_VERSION),$(call set_sdk,$(CONTENTS)/MacOS/$(EXE))) \
	for arch in $(ARCHS); do \
		for f in "$(CONTENTS)"/MacOS/* $(if $(filter appstore,$(DISTRIBUTION)),"$(MCP_APP)/Contents/MacOS/malachi-mcp" "$(MCP_APP)/Contents/MacOS/malachi-mcp-worker"); do \
			lipo "$$f" -verify_arch "$$arch" || { echo "macos: $$f has no $$arch code" >&2; exit 1; }; \
		done; \
	done; \
	cp -R "$(LOCALE_DIR)"/*.lproj "$(CONTENTS)/Resources/"; \
	cp "$(ICNS)" "$(CONTENTS)/Resources/Malachi.icns"; \
	$(if $(filter appstore,$(DISTRIBUTION)),plutil -lint Resources/mas/PrivacyInfo.xcprivacy >/dev/null; cp Resources/mas/PrivacyInfo.xcprivacy "$(CONTENTS)/Resources/";) \
	cp "$(ROOT)/LICENSE" "$(CONTENTS)/Resources/LICENSE.txt"; \
	cp "$(ROOT)/backend/LICENSE" "$(CONTENTS)/Resources/LICENSE-backend.txt"; \
	cp "$(ROOT)/LICENSING.md" "$(CONTENTS)/Resources/LICENSING.md"; \
	cp "$(ROOT)/THIRD-PARTY-NOTICES.md" "$(CONTENTS)/Resources/THIRD-PARTY-NOTICES.md"; \
	sed -e 's/@VERSION@/$(SHORT_VERSION)/g' -e 's/@BUILD@/$(BUILD_NUMBER)/g' -e 's/@FULL_VERSION@/$(VERSION)/g' \
	    -e 's/@BUNDLE_ID@/$(BUNDLE_ID)/g' -e 's/@EXECUTABLE@/$(EXE)/g' \
	    -e 's/@BUNDLE_NAME@/$(BUNDLE_NAME)/g' -e 's/@STABLE_ID@/$(STABLE_ID)/g' \
	    -e 's|@LOCALIZATIONS@|$(LOCALIZATIONS)|g' -e 's/@DISTRIBUTION@/$(DISTRIBUTION)/g' \
	    Resources/Info.plist.in > "$(CONTENTS)/Info.plist"; \
	plutil -lint "$(CONTENTS)/Info.plist" >/dev/null; \
	plutil -lint "$(ENTITLEMENTS)" >/dev/null; \
	plutil -lint "$(MCP_ENTITLEMENTS)" >/dev/null; \
	$(if $(PROFILE),cp "$(PROFILE)" "$(CONTENTS)/embedded.provisionprofile";) \
	printf 'APPL????' > "$(CONTENTS)/PkgInfo"; \
	for f in malachid $(HELPER); do \
		$(CODESIGN) --identifier "$(BUNDLE_ID).$$f" $(if $(HELPER_ENTITLEMENTS),--entitlements "$(HELPER_ENTITLEMENTS)") "$(CONTENTS)/MacOS/$$f"; \
	done; \
	$(if $(filter appstore,$(DISTRIBUTION)),$(CODESIGN) --identifier "$(BUNDLE_ID).malachi-mcp-worker" --entitlements "$(HELPER_ENTITLEMENTS)" "$(MCP_APP)/Contents/MacOS/malachi-mcp-worker"; $(CODESIGN) --entitlements "$(MCP_ENTITLEMENTS)" "$(MCP_APP)",$(CODESIGN) --identifier "$(BUNDLE_ID).malachi-mcp" --entitlements "$(MCP_ENTITLEMENTS)" "$(CONTENTS)/MacOS/malachi-mcp"); \
	$(CODESIGN) --entitlements "$(ENTITLEMENTS)" "$(APP)"; \
	codesign --verify --strict --deep "$(APP)"; \
	echo "built $(APP) ($(BUNDLE_ID), $(VERSION), $(or $(ARCHS),$(shell uname -m)), languages: $(LANGS))"

## dmg: put the bundle into build/Malachi-Mail-<version>-macos-<arch>.dmg beside a link to /Applications
# A Developer ID build signs the image as well. hdiutil on CI runners now
# and then fails with "Resource busy"; a second attempt goes through.
dmg: app
	@test "$(VARIANT)" = release || { echo "macos: a disk image is always VARIANT=release" >&2; exit 1; }
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

## pkg: put the App Store bundle into build/Malachi-Mail-<version>-macos-<arch>.pkg for App Store Connect (DISTRIBUTION=appstore)
# SIGN is an Apple Distribution identity, INSTALLER_SIGN a Mac Installer
# Distribution one ("3rd Party Mac Developer Installer: …"); PROFILE and
# MCP_PROFILE are Mac App Store provisioning profiles. The package installs
# into /Applications; App Store Connect takes it as it is.
INSTALLER_SIGN ?=
PKG           := $(BUILD_DIR)/Malachi-Mail-$(VERSION)-macos-$(DMG_ARCH).pkg
pkg: app
	@test "$(DISTRIBUTION)" = appstore || { echo "macos: pkg needs DISTRIBUTION=appstore" >&2; exit 1; }
	@test -n "$(INSTALLER_SIGN)" || { echo "macos: pkg needs INSTALLER_SIGN='3rd Party Mac Developer Installer: …'" >&2; exit 1; }
	@set -e; rm -f "$(PKG)"; \
	productbuild --component "$(APP)" /Applications --sign "$(INSTALLER_SIGN)" "$(PKG)"; \
	pkgutil --check-signature "$(PKG)" >/dev/null; \
	echo "built $(PKG)"

## validate-pkg: have App Store Connect check the package without uploading it (NOTARY_KEY, NOTARY_KEY_ID, NOTARY_ISSUER)
validate-pkg:
	@test -f "$(PKG)" || { echo "macos: $(PKG) is missing; run make pkg with the same VERSION and ARCHS first" >&2; exit 1; }
	xcrun altool --validate-app -f "$(PKG)" -t macos --api-key "$(NOTARY_KEY_ID)" --api-issuer "$(NOTARY_ISSUER)" --p8-file-path "$(NOTARY_KEY)"

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
