package main

import (
	"fmt"
	"os"

	"github.com/tzone85/nexus-dispatch/internal/cli"
	"github.com/tzone85/nexus-dispatch/internal/nlog"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Initialize logging before any subcommand runs. CLI subcommands may
	// re-call nlog.Setup with the level/format from config; the call is
	// idempotent so the second one is a no-op. This first call ensures
	// startup messages (version-check, plugin loader, etc.) honour
	// NXD_LOG_LEVEL / NXD_LOG_FORMAT environment overrides immediately.
	nlog.Setup(os.Getenv("NXD_LOG_LEVEL"), os.Getenv("NXD_LOG_FORMAT"))
	_ = version // kept reachable for goreleaser ldflags

	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
