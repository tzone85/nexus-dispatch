.PHONY: build test lint clean install release release-snapshot vet check \
        fmt tidy bench coverage-html watch vulncheck doctor notice \
        install-mempalace mempalace-check setup

BINARY=nxd
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"
INSTALL_DIR?=$(shell go env GOPATH)/bin

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/nxd/

test:
	go test ./... -race -coverprofile=coverage.out
	@go tool cover -func=coverage.out | tail -1

# `make coverage-html` writes coverage.html alongside coverage.out and opens
# it in the default browser. Useful for finding uncovered branches.
coverage-html: test
	go tool cover -html=coverage.out -o coverage.html
	@command -v open >/dev/null 2>&1 && open coverage.html || \
	 command -v xdg-open >/dev/null 2>&1 && xdg-open coverage.html || \
	 echo "coverage.html written"

vet:
	go vet ./...

fmt:
	gofmt -w -s .

tidy:
	go mod tidy

# `make bench` runs every benchmark in the repo; useful for the slog and
# event-bus changes where we want to confirm no regressions.
bench:
	go test -run=^$$ -bench=. -benchmem -benchtime=1s ./...

# `make watch` rebuilds + reruns tests on file change. Requires reflex
# (`go install github.com/cespare/reflex@latest`).
watch:
	@command -v reflex >/dev/null 2>&1 || { \
	  echo "reflex not installed; run: go install github.com/cespare/reflex@latest"; exit 1; }
	reflex -r '\.go$$' -s -- sh -c 'make vet test'

# `make vulncheck` runs govulncheck against the dependency graph. CI mirrors
# this in a non-blocking job (.github/workflows/ci.yml::vulncheck).
vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || { \
	  echo "govulncheck not installed; run: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	govulncheck ./...

# `make doctor` proxies to the runtime preflight check.
doctor: build
	./$(BINARY) doctor

# `make install-mempalace` installs the pinned MemPalace version into the
# active Python environment. MemPalace is core infrastructure for NXD —
# the engine mines diffs / review feedback / QA failures into a local
# semantic-memory palace and queries it for prior-work context. It is
# offline-first (ChromaDB local backend, zero API calls) so installing it
# does not weaken the offline guarantee.
install-mempalace:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 not installed"; exit 1; }
	@command -v pip3 >/dev/null 2>&1 || { echo "pip3 not installed"; exit 1; }
	pip3 install -r requirements.txt

# `make mempalace-check` runs an end-to-end smoke through the bridge so
# any future API drift (search --results vs --max-results, wake-up vs
# wakeup, ...) fails the build instead of silently returning empty
# results at runtime.
mempalace-check:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 not installed"; exit 1; }
	python3 scripts/mempalace_bridge.py health | grep -q '"status": "ok"' || \
	  { echo "mempalace bridge unhealthy — run 'make install-mempalace'"; exit 1; }
	@echo "mempalace bridge: ok"

# `make setup` is the one-shot bootstrap a new contributor runs after
# cloning. Installs Go deps, MemPalace, and runs doctor to verify
# everything is reachable.
setup: tidy install-mempalace
	@$(MAKE) doctor || true

# `make notice` regenerates the NOTICE file from go.mod's transitive
# dependencies. Requires go-licenses
# (`go install github.com/google/go-licenses@latest`).
notice:
	@command -v go-licenses >/dev/null 2>&1 || { \
	  echo "go-licenses not installed; run: go install github.com/google/go-licenses@latest"; exit 1; }
	@go-licenses report ./cmd/nxd \
	  --template=scripts/notice.tmpl \
	  --ignore github.com/tzone85/nexus-dispatch \
	  > NOTICE 2>/dev/null && \
	  echo "wrote NOTICE ($$(wc -l < NOTICE | tr -d ' ') lines)"

# `make check` is the single command CI / contributors should run before
# pushing: vet + race tests + build.
check: vet test build

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY) coverage.out coverage.html

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
