.PHONY: build test lint clean install

BINARY=nxd
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"
INSTALL_DIR?=$(shell go env GOPATH)/bin

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/nxd/

test:
	go test ./... -race -coverprofile=coverage.out
	@go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY) coverage.out

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/
