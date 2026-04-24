# Contributing to Nexus Dispatch

Thanks for your interest in contributing to NXD! This guide will help you get started.

## Development Setup

```bash
# Clone
git clone https://github.com/tzone85/nexus-dispatch.git
cd nexus-dispatch

# Build
make build

# Install locally
make install

# Run tests
make test

# Run linter
make lint
```

### Prerequisites

- Go 1.26+
- SQLite (via `mattn/go-sqlite3` — requires cgo)
- tmux (for agent session management)
- Ollama (for local LLM inference) — or configure cloud providers

## Running Tests

```bash
# All tests with race detection
go test -race -count=1 ./...

# Specific package
go test ./internal/engine/ -v

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `golangci-lint` for linting
- Keep functions small and focused
- Write table-driven tests where possible
- Document exported types and functions

## Commit Messages

Follow conventional commits:

```
feat: add Bayesian adaptive routing
fix: prevent hallucination pass-through in QA
test: boost engine coverage to 72%
docs: update architecture overview
refactor: extract monitor polling into separate goroutine
chore: update dependencies
```

## Pull Request Process

1. Fork the repository
2. Create a feature branch (`feat/my-feature` or `fix/my-bug`)
3. Write tests for your changes
4. Ensure all tests pass: `make test`
5. Ensure linting passes: `make lint`
6. Submit a PR with a clear description

### PR Checklist

- [ ] Tests added/updated
- [ ] Documentation updated (if applicable)
- [ ] No new linting warnings
- [ ] Commit messages follow conventional format
- [ ] PR description explains the "why"

## Architecture Overview

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full architecture guide.

Key packages:
- `internal/engine/` — Core orchestration (planner, dispatcher, monitor, reviewer, QA, merger)
- `internal/llm/` — LLM provider abstraction (Ollama, Anthropic, OpenAI, Google)
- `internal/state/` — Event-sourced state management
- `internal/routing/` — Bayesian adaptive agent routing
- `internal/runtime/` — Agent session management (Aider, Claude Code, Codex, Gemma)

## Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include: Go version, OS, NXD version, steps to reproduce, expected vs actual behavior
- For security issues, email security@tzone85.dev (do not use public issues)

## License

By contributing, you agree that your contributions will be licensed under the project's Apache 2.0 License.
