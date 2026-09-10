GO       ?= go
WAILS    ?= $(shell go env GOPATH)/bin/wails
GOLANGCI ?= $(shell go env GOPATH)/bin/golangci-lint

.PHONY: all test test-short lint fmt build dev tidy clean

all: test lint

## test: run the full unit test suite (includes the build smoke test)
test:
	$(GO) test ./...

## test-short: run unit tests without the build/vet smoke test
test-short:
	$(GO) test -short ./...

## lint: run golangci-lint over the whole module
lint:
	$(GOLANGCI) run ./...

## fmt: format all Go sources
fmt:
	$(GO) fmt ./...

## build: produce the macOS .app bundle via Wails
build:
	$(WAILS) build

## dev: run the app with live reload
dev:
	$(WAILS) dev

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

## clean: remove build artefacts
clean:
	rm -rf build/bin frontend/dist/assets frontend/dist/index.html
