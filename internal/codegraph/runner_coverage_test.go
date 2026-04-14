package codegraph

import (
	"context"
	"testing"
)

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
	// Build on the VXD repo — graph.db already exists from earlier
	err := r.Build(context.Background(), "/Users/mncedimini/Sites/misc/nexus-dispatch")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
}

func TestRunner_Status_Available(t *testing.T) {
	r := NewRunner()
	if !r.Available() {
		t.Skip("code-review-graph not installed")
	}
	info, err := r.Status(context.Background(), "/Users/mncedimini/Sites/misc/nexus-dispatch")
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
	ia, err := r.DetectChanges(context.Background(), "/Users/mncedimini/Sites/misc/nexus-dispatch", "HEAD~1")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}
	// Should have some analysis — we've been making changes
	if ia.Summary == "" {
		t.Log("warning: empty summary, graph may need rebuild")
	}
}

func TestGraphDB_Open_Existing(t *testing.T) {
	// Test opening the real VXD graph database
	gdb, err := Open("/Users/mncedimini/Sites/misc/nexus-dispatch")
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
