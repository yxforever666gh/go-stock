SHELL := /bin/bash

GO_PACKAGES := $(shell go list ./... | grep -v '/frontend/node_modules/')

.PHONY: ci test test-go test-tools test-frontend test-go-race test-integration lint lint-go lint-frontend openapi-generate openapi-check build-web build-frontend build-web-binary dev dev-web run-web

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
	RUN_INTEGRATION_TESTS=1 go test $(GO_PACKAGES)

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

ci: openapi-check test lint build-web

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
