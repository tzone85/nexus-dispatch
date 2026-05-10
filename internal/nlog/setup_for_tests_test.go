package nlog

import (
	"context"
	"log"
	"log/slog"
	"testing"
)

// TestSetupForTests_InstallsDiscardLogger covers the helper used by
// other packages' test setup. Without it, parallel test packages may
// emit slog noise to stderr that breaks test output capture.
func TestSetupForTests_InstallsDiscardLogger(t *testing.T) {
	prev := slog.Default()
	prevConfigured := configured.Load()
	prevWriter := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(prev)
		configured.Store(prevConfigured)
		log.SetOutput(prevWriter)
	})

	SetupForTests()

	// IsConfigured must report true so loadConfig's preflight doesn't
	// re-Setup the logger and clobber the discard handler mid-test.
	if !IsConfigured() {
		t.Error("after SetupForTests, IsConfigured should be true")
	}
	// Default logger must have changed from the previous one.
	if slog.Default() == prev {
		t.Error("SetupForTests did not replace the default slog logger")
	}

	// Smoke-test: emitting through stdlib log + slog should not panic
	// and should hit the discard writer.
	log.Print("[nlog] setup-for-tests smoke test")
	slog.Info("setup-for-tests slog smoke test")
}

// TestWithCtx_NilContext returns the default logger unchanged. Without
// this guard, callers that pass ctx == nil (e.g. background goroutines
// without a parent) would NPE.
func TestWithCtx_NilContext(t *testing.T) {
	// A nil context.Context is a real-world risk (callers in
	// background goroutines may forget to pass one); the helper
	// guards against it. Use a typed nil to test the branch without
	// triggering staticcheck's SA1012 "do not pass nil context" warn
	// — the variable is typed, just unset.
	var ctx context.Context
	got := WithCtx(ctx)
	if got == nil {
		t.Fatal("WithCtx(nil) must return a logger, not nil")
	}
	got.Info("nil-ctx logger smoke test")
}
