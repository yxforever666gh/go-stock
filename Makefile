SHELL := /bin/bash

.PHONY: test test-go lint lint-go lint-frontend build-web build-frontend dev-web build-desktop

test: test-go

test-go:
	go test ./...

lint: lint-go lint-frontend

lint-go:
	golangci-lint run ./...

lint-frontend:
	cd frontend && npm run lint

build-web: build-frontend

build-frontend:
	cd frontend && npm run build

dev-web:
	./scripts/dev-web.sh

build-desktop:
	./scripts/build.sh
