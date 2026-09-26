GO ?= go
SQLC ?= sqlc
ATLAS ?= atlas
GOLANGCI_LINT_VERSION ?= v2.14.0
GOVULNCHECK_VERSION ?= v1.8.0

MODULE_PATH := $(shell $(GO) list -m)
VERSION ?= dev
COMMIT ?= unknown
BUILD_DATE ?= unknown
BUILD_LDFLAGS := -s -w -X '$(MODULE_PATH)/internal/buildinfo.version=$(VERSION)' -X '$(MODULE_PATH)/internal/buildinfo.commit=$(COMMIT)' -X '$(MODULE_PATH)/internal/buildinfo.buildDate=$(BUILD_DATE)'

.PHONY: dev test test-integration test-integration-external fmt lint vet build vuln check generate generate-check
.PHONY: db-up db-down migrate-hash migrate-status migrate-up migrate-diff

dev:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

test-integration:
	cd test/integration && $(GO) test ./...

test-integration-external:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required" && exit 1)
	cd test/integration && DATABASE_URL="$(DATABASE_URL)" $(GO) test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(BUILD_LDFLAGS)" -o bin/api ./cmd/api

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

generate:
	$(SQLC) generate

generate-check:
	$(SQLC) generate
	git diff --exit-code -- internal/modules/example/store/sqlc

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate-hash:
	$(ATLAS) migrate hash --dir "file://migrations"

migrate-status:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required" && exit 1)
	@$(ATLAS) migrate status --url "$(DATABASE_URL)" --dir "file://migrations"

migrate-up:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required" && exit 1)
	@$(ATLAS) migrate apply --url "$(DATABASE_URL)" --dir "file://migrations"

migrate-diff:
	@test -n "$(NAME)" || (echo "NAME is required" && exit 1)
	@test -n "$(DATABASE_DEV_URL)" || (echo "DATABASE_DEV_URL is required" && exit 1)
	@$(ATLAS) migrate diff "$(NAME)" 		--dir "file://migrations" 		--to "file://sql/schema/schema.sql" 		--dev-url "$(DATABASE_DEV_URL)"

check: vet test build
