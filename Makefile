GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

# denarix's version is MAJOR.MINOR (from the VERSION file) plus a PATCH
# that's just the repo's total commit count, so it advances on its own as
# commits land instead of needing to be hand-bumped. Shared by both
# binaries (denarix, dxctl).
VERSION_PKG := github.com/silverbp/denarix/internal/version
VERSION     := $(shell cat VERSION).$(shell git rev-list --count HEAD 2>/dev/null || echo 0)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X '$(VERSION_PKG).Version=$(VERSION)' -X '$(VERSION_PKG).GitCommit=$(GIT_COMMIT)' -X '$(VERSION_PKG).BuildDate=$(BUILD_DATE)'

.PHONY: generate proto sqlc migrate-up db-up db-down run build denarix dxctl install test

generate: proto sqlc

proto:
	buf generate proto

sqlc:
	sqlc generate

db-up:
	docker compose up -d db seaweedfs

db-down:
	docker compose down

migrate-up: db-up
	@until docker compose exec -T db pg_isready -U denarix >/dev/null 2>&1; do sleep 1; done
	DENARIX_POSTGRES_DSN=postgres://denarix:denarix@localhost:5432/denarix?sslmode=disable go run ./cmd/denarix migrate

build:
	go build ./...

denarix:
	go build -ldflags "$(LDFLAGS)" -o bin/denarix ./cmd/denarix

dxctl:
	go build -ldflags "$(LDFLAGS)" -o bin/dxctl ./cmd/dxctl

# Same as `dxctl`, but installs to $GOBIN (plain `go install ./cmd/dxctl`
# skips the Makefile entirely, so it can't stamp version info - always
# build dxctl through this target, not a bare `go install`, unless you
# don't care which version string it reports).
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/dxctl

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/denarix

test:
	go test ./...
