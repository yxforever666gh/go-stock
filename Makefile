SHELL := /bin/bash

GO_PACKAGES := . ./backend/... ./internal/... ./cmd/...
POWERSHELL ?= pwsh

.PHONY: ci verify-fast verify-domain verify-release test test-go test-tools test-frontend test-go-race test-integration lint lint-go lint-frontend openapi-generate openapi-check build-web build-frontend build-web-binary dev dev-web run-web

test: test-go test-tools test-frontend

test-go:
	go test $(GO_PACKAGES)

test-tools:
	cd tools/network-audit && go test ./...

test-frontend:
	cd frontend && npm run test:runtime

test-go-race:
	go test -race $(GO_PACKAGES)

test-integration:
	@echo "Integration tests are disabled until live network and production database access are isolated."
	@exit 1

lint: lint-go lint-frontend

lint-go:
	go vet $(GO_PACKAGES)
	go mod tidy -diff
	cd tools/network-audit && go mod tidy -diff
	test -z "$$(find . -path './frontend/node_modules' -prune -o -path './third_party' -prune -o -path './tools/network-audit' -prune -o -name '*.go' -print | xargs gofmt -l)"
	test -z "$$(find tools/network-audit -name '*.go' -print | xargs gofmt -l)"

lint-frontend:
	cd frontend && npm run lint

openapi-generate:
	go run ./cmd/openapi-contract -write

openapi-check:
	go run ./cmd/openapi-contract

build-web: build-frontend

build-frontend:
	cd frontend && npm run build

verify-fast:
	$(POWERSHELL) -NoProfile -File scripts/verify.ps1 -Tier fast $(VERIFY_ARGS)

verify-domain:
	@test -n "$(DOMAIN)" || (echo "DOMAIN is required: data|research|research2|migrations|frontend|api|tools"; exit 2)
	$(POWERSHELL) -NoProfile -File scripts/verify.ps1 -Tier domain -Domain $(DOMAIN)

verify-release:
	$(POWERSHELL) -NoProfile -File scripts/verify.ps1 -Tier release

ci: verify-release

dev: dev-web

dev-web:
	./scripts/dev-web.sh

run-web:
	GO_STOCK_WEB_ADDR=$${GO_STOCK_WEB_ADDR:-127.0.0.1:34115} \
	GO_STOCK_DB_LOG_LEVEL=$${GO_STOCK_DB_LOG_LEVEL:-silent} \
	GO_STOCK_LOG_LEVEL=$${GO_STOCK_LOG_LEVEL:-warn} \
	go run .

build-web-binary:
	go build -o go-stock-web .
