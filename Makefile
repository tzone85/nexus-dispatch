.PHONY: build test lint clean install release release-snapshot vet check

BINARY=nxd
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"
INSTALL_DIR?=$(shell go env GOPATH)/bin

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/nxd/

test:
	go test ./... -race -coverprofile=coverage.out
	@go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

# `make check` is the single command CI / contributors should run before
# pushing: vet + race tests + build.
check: vet test build

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY) coverage.out

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/

# S3-1: one-shot release pipeline. Requires goreleaser to be installed.
# Tags should already be pushed; goreleaser reads VERSION from the latest tag.
release:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed (brew install goreleaser)"; exit 1; }
	goreleaser release --clean

# Test the release pipeline locally without publishing.
release-snapshot:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed (brew install goreleaser)"; exit 1; }
	goreleaser release --snapshot --clean
