package engine

import (
	"strings"
	"testing"
)

func TestRecoveryRegistry_RegisterAddsStrategyAtEnd(t *testing.T) {
	defer ResetRecoveryStrategies(nil)

	called := false
	RegisterRecoveryStrategy(RecoveryStrategy{
		Name:  "custom_panic",
		Match: func(s string) bool { called = true; return strings.Contains(s, "boom!!!") },
		Hint:  "custom panic hint",
	})

	// Built-in patterns should still match — registry append goes last.
	if got := AnalyzeFailure("undefined: Foo", ""); !strings.Contains(got, "undefined symbol") {
		t.Errorf("built-in undefined pattern lost after register: got %q", got)
	}

	// And the new strategy should fire when no built-in matches.
	got := AnalyzeFailure("explosion: boom!!!", "")
	if !called {
		t.Error("custom strategy Match was not invoked")
	}
	if got != "custom panic hint" {
		t.Errorf("custom strategy hint not returned: got %q", got)
	}
}

func TestRecoveryRegistry_ResetRestoresDefaults(t *testing.T) {
	ResetRecoveryStrategies([]RecoveryStrategy{
		{
			Name:  "only",
			Match: func(s string) bool { return true },
			Hint:  "exclusive",
		},
	})
	if got := AnalyzeFailure("undefined: Foo", ""); got != "exclusive" {
		t.Errorf("expected exclusive registry to win, got %q", got)
	}

	ResetRecoveryStrategies(nil) // restore defaults

	if got := AnalyzeFailure("undefined: Foo", ""); !strings.Contains(got, "undefined symbol") {
		t.Errorf("default registry not restored: got %q", got)
	}
}

func TestRecoveryStrategies_ReturnsSnapshot(t *testing.T) {
	snap := RecoveryStrategies()
	if len(snap) == 0 {
		t.Fatal("default registry should be non-empty")
	}
	// Mutating the snapshot must not affect the registry.
	snap[0].Hint = "MUTATED"
	fresh := RecoveryStrategies()
	if fresh[0].Hint == "MUTATED" {
		t.Error("RecoveryStrategies returned a live reference, not a copy")
	}
}
