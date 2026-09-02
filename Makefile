# Malachi Mail — build entry points.
#
# Targets: build, run-dev (run), run-backend, run-frontend, test, lint, clean,
# flatpak (plus helpers). `make help` lists them.
# Everything Go-related is built inside the Toolbx container; `make flatpak`
# is meant to be run on the host where flatpak-builder lives.

APP_ID      := io.github.schotek.Malachi
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR   := build
GO          ?= go
GOFLAGS     ?=
LDFLAGS     := -X main.version=$(VERSION)

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

.PHONY: all build backend ui blueprint data schemas run run-dev run-backend run-frontend test lint fmt vet clean flatpak flatpak-run help

all: build

## build: compile backend daemon and UI into ./build
build: backend ui schemas

backend: $(BUILD_DIR)/malachid

$(BUILD_DIR)/malachid: $(shell find backend -name '*.go' -o -name '*.sql' -o -name go.mod)
	@mkdir -p $(BUILD_DIR)
	cd backend && $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o ../$@ ./cmd/malachid

ui: blueprint $(BUILD_DIR)/malachi

$(BUILD_DIR)/malachi: $(BLP_OUT) $(shell find ui -name '*.go' -o -name go.mod) backend/pkg/api/*.go
	@mkdir -p $(BUILD_DIR)
	cd ui && $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o ../$@ .

## blueprint: compile Blueprint (.blp) files to GtkBuilder XML
blueprint: $(BLP_OUT)

ui/data/ui/%.ui: ui/data/ui/%.blp
	blueprint-compiler compile --output $@ $<

## data: render desktop/metainfo templates (substitutes @APP_ID@ and @VERSION@)
data: $(DATA_OUT)

data/%: data/%.in
	sed -e 's/@APP_ID@/$(APP_ID)/g' -e 's/@VERSION@/$(VERSION)/g' $< > $@

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
run-frontend: ui schemas
	$(SCHEMA_ENV) ./$(BUILD_DIR)/malachi $(ARGS)

## test: run Go tests for both modules
test: blueprint schemas
	cd backend && $(GO) test ./...
	cd ui && $(SCHEMA_ENV) GSETTINGS_BACKEND=memory $(GO) test ./...

## lint: golangci-lint if installed, otherwise go vet; validate Blueprint, schema and desktop/metainfo files
lint: blueprint data schemas vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		(cd backend && golangci-lint run ./...); \
		(cd ui && golangci-lint run ./...); \
	else \
		echo "golangci-lint not installed; ran go vet only"; \
	fi
	@if command -v desktop-file-validate >/dev/null 2>&1; then \
		desktop-file-validate data/$(APP_ID).desktop; fi
	@if command -v appstreamcli >/dev/null 2>&1; then \
		appstreamcli validate --no-net data/$(APP_ID).metainfo.xml; fi

vet:
	cd backend && $(GO) vet ./...
	cd ui && $(GO) vet ./...

fmt:
	cd backend && gofmt -w .
	cd ui && gofmt -w .
	blueprint-compiler format --fix $(BLP_SRC)

## clean: remove build output and generated files
clean:
	rm -rf $(BUILD_DIR) $(BLP_OUT) $(DATA_OUT)
	rm -rf .flatpak-builder repo

## flatpak: build the Flatpak (run on the host, needs flatpak-builder)
flatpak:
	flatpak-builder --force-clean --user --install-deps-from=flathub \
		--repo=repo $(BUILD_DIR)/flatpak packaging/flatpak/$(APP_ID).yml

flatpak-run:
	flatpak-builder --run $(BUILD_DIR)/flatpak packaging/flatpak/$(APP_ID).yml malachi

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/^## /  /'
