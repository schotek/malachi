# Malachi Mail — build entry points.
#
# Targets: build, run, test, lint, clean, flatpak (plus helpers).
# Everything Go-related is built inside the Toolbx container; `make flatpak`
# is meant to be run on the host where flatpak-builder lives.

APP_ID      := io.github.GITHUB_USER.Malachi
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR   := build
GO          ?= go
GOFLAGS     ?=
LDFLAGS     := -X main.version=$(VERSION)

BLP_SRC     := $(wildcard ui/data/ui/*.blp)
BLP_OUT     := $(BLP_SRC:.blp=.ui)

DATA_IN     := $(wildcard data/*.in)
DATA_OUT    := $(DATA_IN:.in=)

.PHONY: all build backend ui blueprint data run test lint fmt vet clean flatpak flatpak-run help

all: build

## build: compile backend daemon and UI into ./build
build: backend ui

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

## run: build everything and start backend + UI together
run: build
	./scripts/dev-run.sh

## test: run Go tests for both modules
test: blueprint
	cd backend && $(GO) test ./...
	cd ui && $(GO) test ./...

## lint: golangci-lint if installed, otherwise go vet; validate Blueprint and desktop/metainfo files
lint: blueprint data vet
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
