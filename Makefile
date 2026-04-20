SHELL := /bin/bash

.PHONY: ci test test-go test-integration test-desktop lint lint-go lint-frontend build-web build-frontend dev-web build-desktop

test: test-go

test-go:
	go test ./...

test-integration:
	RUN_INTEGRATION_TESTS=1 go test ./...

test-desktop:
	RUN_DESKTOP_TESTS=1 go test . -run TestGetScreenResolution -count=1

lint: lint-go lint-frontend

lint-go:
	golangci-lint run ./...

lint-frontend:
	cd frontend && npm run lint

build-web: build-frontend

build-frontend:
	cd frontend && npm run build

ci: test lint build-web

dev-web:
	./scripts/dev-web.sh

build-desktop:
	./scripts/build.sh
