GO ?= go
NPM ?= npm
GO_MODULE_ENV ?= GO111MODULE=on

.PHONY: test build frontend-build ui-smoke gofmt verify-boundaries

gofmt:
	$(GO)fmt -w ./cmd ./internal ./web

test: verify-boundaries
	$(GO_MODULE_ENV) $(GO) test ./...

verify-boundaries:
	@! $(GO_MODULE_ENV) $(GO) list -deps ./internal/importer | grep -i notion

frontend-build:
	$(NPM) run build --prefix webapp

ui-smoke: frontend-build
	$(NPM) run smoke --prefix webapp

build: frontend-build
	$(GO_MODULE_ENV) $(GO) build -o bin/memos-importer ./cmd/server
