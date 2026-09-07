# Malachi Mail — build entry points.
#
# Targets: build, run-dev (run), run-backend, run-frontend, test, lint, clean,
# flatpak (plus helpers). `make help` lists them.
# Everything Go-related is built inside the Toolbx container; `make flatpak`
# is meant to be run on the host where flatpak-builder lives.

APP_ID      := io.github.schotek.Malachi
# Release versions are the git tags, "v0.1.0" style; the leading v is cut
# because AppStream and Flatpak want a bare number. Between tags this is
# "0.1.0-3-gabc1234", and without any tag the bare commit (see
# docs/releasing.md).
# The Flatpak build gets a copy of the tree without .git, so git describe
# has nothing to read there; it falls back to the .version file that
# `make flatpak` and the CI workflow write.
VERSION     ?= $(shell { git describe --tags --always --dirty 2>/dev/null \
                 || cat .version 2>/dev/null || echo dev; } | sed 's/^v//')
BUILD_DIR   := build
GO          ?= go
GOFLAGS     ?=
LDFLAGS     := -X main.version=$(VERSION)

# Install prefix (scripts/build.sh passes /app for Flatpak). The UI needs the
# locale directory compiled in to find its .mo files when installed.
PREFIX      ?= /usr/local
LOCALEDIR   ?= $(PREFIX)/share/locale
LDFLAGS_UI  := $(LDFLAGS) -X main.localeDir=$(LOCALEDIR)

# Translations. po/POTFILES lists the Go sources; Blueprint output,
# gschema, desktop and metainfo are extracted through gettext's ITS rules.
# xgettext has no Go mode; C mode handles Go's double-quoted literals.
PO_DIR      := po
POT         := $(PO_DIR)/malachi.pot
LINGUAS     := $(shell grep -v '^\#' $(PO_DIR)/LINGUAS 2>/dev/null)
PO_FILES    := $(foreach l,$(LINGUAS),$(PO_DIR)/$(l).po)
LOCALE_DIR  := $(BUILD_DIR)/locale
MO_OUT      := $(foreach l,$(LINGUAS),$(LOCALE_DIR)/$(l)/LC_MESSAGES/malachi.mo)
LOCALE_ENV  := MALACHI_LOCALE_DIR=$(CURDIR)/$(LOCALE_DIR)
ICON_SRC    := ui/data/icons/$(APP_ID).svg
ICON_ENV    := MALACHI_ICON_DIR=$(CURDIR)/$(dir $(ICON_SRC))
POTFILES_GO := $(shell grep -v '^\#' $(PO_DIR)/POTFILES 2>/dev/null)
XGETTEXT    := xgettext --from-code=UTF-8 --package-name=malachi \
               --msgid-bugs-address=https://github.com/schotek/malachi/issues \
               --copyright-holder="Vladislav Janeček" --add-comments=TRANSLATORS

BLP_SRC     := $(wildcard ui/data/ui/*.blp)
BLP_OUT     := $(BLP_SRC:.blp=.ui)

DATA_IN     := $(wildcard data/*.in)
DATA_OUT    := $(DATA_IN:.in=)

# GSettings schema compiled for uninstalled (development) runs. GLib reads
# GSETTINGS_SCHEMA_DIR once at process start as a colon-separated list that is
# searched before the XDG data dirs, so run/test targets export it.
SCHEMA_SRC  := data/$(APP_ID).gschema.xml
SCHEMA_DIR  := $(BUILD_DIR)/glib-2.0/schemas
SCHEMA_OUT  := $(SCHEMA_DIR)/gschemas.compiled
SCHEMA_ENV  := GSETTINGS_SCHEMA_DIR=$(CURDIR)/$(SCHEMA_DIR)$(if $(GSETTINGS_SCHEMA_DIR),:$(GSETTINGS_SCHEMA_DIR))

# Notification sound needs gsound (gsound-devel). Without it the UI is built
# with -tags nosound and "Play Sound" does nothing.
ifeq ($(shell pkg-config --exists gsound 2>/dev/null && echo yes),yes)
UI_TAGS     :=
else
UI_TAGS     := -tags nosound
$(warning gsound not found via pkg-config; building the UI without notification sound (install gsound-devel))
endif

.PHONY: all build backend ui blueprint data schemas locale pot po run run-dev run-backend run-frontend test lint fmt vet clean flatpak flatpak-run help

all: build

## build: compile backend daemon and UI into ./build
build: backend ui schemas locale

backend: $(BUILD_DIR)/malachid

$(BUILD_DIR)/malachid: $(shell find backend -name '*.go' -o -name '*.sql' -o -name go.mod)
	@mkdir -p $(BUILD_DIR)
	cd backend && $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o ../$@ ./cmd/malachid

ui: blueprint $(BUILD_DIR)/malachi

$(BUILD_DIR)/malachi: $(BLP_OUT) $(shell find ui -name '*.go' -o -name go.mod) backend/pkg/api/*.go
	@mkdir -p $(BUILD_DIR)
	cd ui && $(GO) build $(GOFLAGS) $(UI_TAGS) -ldflags '$(LDFLAGS_UI)' -o ../$@ .

## blueprint: compile Blueprint (.blp) files to GtkBuilder XML
blueprint: $(BLP_OUT)

ui/data/ui/%.ui: ui/data/ui/%.blp
	blueprint-compiler compile --output $@ $<

## data: render desktop/metainfo templates (substitutes @APP_ID@ and @VERSION@, merges translations)
data: $(DATA_OUT)

data/%.desktop: data/%.desktop.in $(PO_FILES) $(PO_DIR)/LINGUAS
	sed -e 's/@APP_ID@/$(APP_ID)/g' -e 's/@VERSION@/$(VERSION)/g' $< > $@.tmp
	msgfmt --desktop --template=$@.tmp -d $(PO_DIR) --keyword= --keyword=GenericName --keyword=Comment --keyword=Keywords -o $@
	rm -f $@.tmp

# The <releases> block is generated from NEWS, which is the single source
# of the release notes; the template carries none. Descriptions are not
# marked translatable (-t 0), so a release does not churn the catalogues.
# msgfmt picks the ITS rules from the template's file name, so every
# intermediate file must still end in .metainfo.xml (they live in build/).
data/%.metainfo.xml: data/%.metainfo.xml.in NEWS $(PO_FILES) $(PO_DIR)/LINGUAS
	@mkdir -p $(BUILD_DIR)/news
	sed -e 's/@APP_ID@/$(APP_ID)/g' -e 's/@VERSION@/$(VERSION)/g' $< > $(BUILD_DIR)/$(notdir $@)
	@if command -v appstreamcli >/dev/null 2>&1; then \
		appstreamcli news-to-metainfo --format=text -t 0 NEWS \
			$(BUILD_DIR)/$(notdir $@) $(BUILD_DIR)/news/$(notdir $@); \
	else \
		echo "$@: appstreamcli not found; building without release notes" >&2; \
		cp $(BUILD_DIR)/$(notdir $@) $(BUILD_DIR)/news/$(notdir $@); \
	fi
	msgfmt --xml --template=$(BUILD_DIR)/news/$(notdir $@) -d $(PO_DIR) -o $@

## locale: compile po/*.po into build/locale (for uninstalled runs and install)
locale: $(MO_OUT)

$(LOCALE_DIR)/%/LC_MESSAGES/malachi.mo: $(PO_DIR)/%.po
	@mkdir -p $(dir $@)
	msgfmt --check -o $@ $<

## pot: regenerate po/malachi.pot from Go, Blueprint, gschema, desktop and metainfo
pot: blueprint
	$(XGETTEXT) --language=C --keyword=T --keyword=N:1,2 --keyword=C:1c,2 -o $(POT) $(POTFILES_GO)
	$(XGETTEXT) -j -o $(POT) $(BLP_OUT) data/$(APP_ID).gschema.xml
	$(XGETTEXT) -j -o $(POT) --language=Desktop --keyword= --keyword=GenericName --keyword=Comment --keyword=Keywords data/$(APP_ID).desktop.in
	$(XGETTEXT) -j -o $(POT) --its=/usr/share/gettext/its/metainfo.its data/$(APP_ID).metainfo.xml.in

## po: update every po/*.po from the template (run after pot)
po: pot
	@for l in $(LINGUAS); do msgmerge --update --backup=none --quiet $(PO_DIR)/$$l.po $(POT); done

## schemas: compile the GSettings schema into build/glib-2.0/schemas (for uninstalled runs)
schemas: $(SCHEMA_OUT)

$(SCHEMA_OUT): $(SCHEMA_SRC)
	@mkdir -p $(SCHEMA_DIR)
	cp $< $(SCHEMA_DIR)/
	glib-compile-schemas --strict --targetdir=$(SCHEMA_DIR) $(SCHEMA_DIR)

## run-dev: build everything and start backend + UI together (alias: run)
run-dev: build
	./scripts/dev-run.sh

run: run-dev

## run-backend: build and start only the daemon in the foreground (Ctrl+C to stop)
run-backend: backend
	./$(BUILD_DIR)/malachid $(ARGS)

## run-frontend: build and start only the UI (connects to a running malachid, or shows a banner)
run-frontend: ui schemas locale
	$(SCHEMA_ENV) $(LOCALE_ENV) $(ICON_ENV) ./$(BUILD_DIR)/malachi $(ARGS)

## test: run Go tests for both modules
test: blueprint schemas
	cd backend && $(GO) test ./...
	cd ui && $(SCHEMA_ENV) GSETTINGS_BACKEND=memory $(GO) test $(UI_TAGS) ./...

## lint: golangci-lint if installed, otherwise go vet; validate Blueprint, schema and desktop/metainfo files
lint: blueprint data schemas vet
	@for l in $(LINGUAS); do msgfmt --check --statistics -o /dev/null $(PO_DIR)/$$l.po; done
	@# The committed template must match the sources (ignoring the timestamp).
	@$(MAKE) --no-print-directory POT=$(BUILD_DIR)/malachi.check.pot pot >/dev/null
	@grep -v POT-Creation-Date $(POT) > $(BUILD_DIR)/malachi.pot.a; \
	grep -v POT-Creation-Date $(BUILD_DIR)/malachi.check.pot > $(BUILD_DIR)/malachi.pot.b; \
	if ! diff -q $(BUILD_DIR)/malachi.pot.a $(BUILD_DIR)/malachi.pot.b >/dev/null; then \
		echo "po/malachi.pot is out of date: run 'make po'"; exit 1; fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		(cd backend && golangci-lint run ./...); \
		(cd ui && golangci-lint run $(UI_TAGS) ./...); \
	else \
		echo "golangci-lint not installed; ran go vet only"; \
	fi
	@if command -v desktop-file-validate >/dev/null 2>&1; then \
		desktop-file-validate data/$(APP_ID).desktop; fi
	@# The desktop file names this icon and appstreamcli compose insists on
	@# it; without it the Flatpak build fails late with icon-not-found.
	@test -f $(ICON_SRC) || { echo "$(ICON_SRC) is missing (data/$(APP_ID).desktop names it as Icon=)"; exit 1; }
	@if command -v appstreamcli >/dev/null 2>&1; then \
		appstreamcli validate --no-net data/$(APP_ID).metainfo.xml; fi

vet:
	cd backend && $(GO) vet ./...
	cd ui && $(GO) vet $(UI_TAGS) ./...

fmt:
	cd backend && gofmt -w .
	cd ui && gofmt -w .
	blueprint-compiler format --fix $(BLP_SRC)

## clean: remove build output and generated files
clean:
	rm -rf $(BUILD_DIR) $(BLP_OUT) $(DATA_OUT)
	rm -rf .flatpak-builder repo

## vendor: fetch dependencies into backend/vendor and ui/vendor (for offline builds)
vendor:
	./scripts/flatpak-vendor.sh

## flatpak: build the Flatpak (run on the host, needs flatpak-builder)
flatpak: vendor
	@echo $(VERSION) > .version
	flatpak-builder --force-clean --user --install-deps-from=flathub \
		--repo=repo $(BUILD_DIR)/flatpak packaging/flatpak/$(APP_ID).yml

flatpak-run:
	flatpak-builder --run $(BUILD_DIR)/flatpak packaging/flatpak/$(APP_ID).yml malachi

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/^## /  /'
