package scratchboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNew_MkdirFailureWraps covers the error path in New() where
// the parent directory can't be created (e.g. the parent of the
// scratchboard path is itself a regular file). Without the test the
// wrapping in fmt.Errorf stays uncovered and a future regression in
// the prefix would silently strip context from operator-facing
// errors.
func TestNew_MkdirFailureWraps(t *testing.T) {
	tmp := t.TempDir()
	// Plant a regular file where the parent dir would need to be a
	// directory — MkdirAll will refuse.
	blocker := filepath.Join(tmp, "block")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	_, err := New(filepath.Join(blocker, "subdir", "scratch.jsonl"))
	if err == nil {
		t.Fatal("expected error when parent dir cannot be created")
	}
	if !strings.Contains(err.Error(), "create scratchboard dir") {
		t.Errorf("error missing 'create scratchboard dir' prefix: %v", err)
	}
}
