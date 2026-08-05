SHELL := /bin/bash

GO_PACKAGES := $(shell go list ./... | grep -v '/frontend/node_modules/')

.PHONY: ci test test-go test-go-race test-integration test-desktop lint lint-go lint-frontend openapi-generate openapi-check build-web build-frontend build-web-binary dev dev-web run-web build-desktop

test: test-go

test-go:
	go test $(GO_PACKAGES)

test-go-race:
	go test -race $(GO_PACKAGES)

test-integration:
	RUN_INTEGRATION_TESTS=1 go test $(GO_PACKAGES)

test-desktop:
	RUN_DESKTOP_TESTS=1 go test . -run TestGetScreenResolution -count=1

lint: lint-go lint-frontend

lint-go:
	go vet ./...
	go mod tidy -diff
	test -z "$$(gofmt -l $$(git diff --name-only --diff-filter=ACMR 1.5.0 HEAD -- '*.go'))"

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
	go run -tags webonly . --web

build-web-binary:
	go build -tags webonly -o go-stock-web .

build-desktop:
	./scripts/build.sh
