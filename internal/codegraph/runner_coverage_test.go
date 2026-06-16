package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot returns the absolute path to the repo root, walking up from
// this test file until it finds go.mod. Keeps the live-binary tests
// portable across contributors and CI runners (previously hardcoded a
// single developer's checkout path).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod walking up from test file")
		}
		dir = parent
	}
}

func TestRunner_Build_Unavailable(t *testing.T) {
	r := &Runner{} // no binary
	err := r.Build(context.Background(), "/tmp")
	if err == nil {
		t.Error("expected error when binary not installed")
	}
}

func TestRunner_Update_Unavailable(t *testing.T) {
	r := &Runner{} // no binary
	err := r.Update(context.Background(), "/tmp", "HEAD~1")
	if err == nil {
		t.Error("expected error when binary not installed")
	}
}

func TestRunner_Update_WithEmptyBase(t *testing.T) {
	r := &Runner{} // no binary
	err := r.Update(context.Background(), "/tmp", "")
	if err == nil {
		t.Error("expected error when binary not installed")
	}
}

func TestRunner_Build_Available(t *testing.T) {
	r := NewRunner()
	if !r.Available() {
		t.Skip("code-review-graph not installed")
	}
	// Build on the current repo — graph.db is created if absent.
	err := r.Build(context.Background(), repoRoot(t))
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
}

func TestRunner_Status_Available(t *testing.T) {
	r := NewRunner()
	if !r.Available() {
		t.Skip("code-review-graph not installed")
	}
	info, err := r.Status(context.Background(), repoRoot(t))
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if info.NodeCount == 0 {
		t.Error("expected non-zero node count from existing graph")
	}
}

func TestRunner_DetectChanges_Available(t *testing.T) {
	r := NewRunner()
	if !r.Available() {
		t.Skip("code-review-graph not installed")
	}
	ia, err := r.DetectChanges(context.Background(), repoRoot(t), "HEAD~1")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}
	// Should have some analysis — we've been making changes
	if ia.Summary == "" {
		t.Log("warning: empty summary, graph may need rebuild")
	}
}

func TestGraphDB_Open_Existing(t *testing.T) {
	gdb, err := Open(repoRoot(t))
	if err != nil {
		t.Skip("no graph database available")
	}
	defer gdb.Close()

	info, err := gdb.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if info.NodeCount == 0 {
		t.Error("expected non-zero node count")
	}
	if info.FileCount == 0 {
		t.Error("expected non-zero file count")
	}
	if len(info.Languages) == 0 {
		t.Error("expected at least one language")
	}
}
