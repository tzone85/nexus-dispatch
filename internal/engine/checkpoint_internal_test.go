package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteCheckpoint_TempFileCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")

	cp := Checkpoint{
		ReqID:        "r-001",
		Phase:        PhaseMerging,
		WaveNumber:   1,
		MergingStory: "s-001",
		Timestamp:    time.Now(),
		PID:          99,
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	// Verify temp file was cleaned up
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("expected temp file to be cleaned up after rename")
	}
}

func TestReadCheckpoint_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json"), 0o644)

	_, err := ReadCheckpoint(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestClearCheckpoint_NonExistent(t *testing.T) {
	// Should not panic
	ClearCheckpoint(filepath.Join(t.TempDir(), "does-not-exist.json"))
}

func TestWriteCheckpoint_MultipleAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")

	cp := Checkpoint{
		ReqID:      "r-002",
		Phase:      PhaseMonitoring,
		WaveNumber: 3,
		ActiveAgents: []CheckpointAgent{
			{StoryID: "s-a", SessionName: "vxd-s-a", WorktreePath: "/tmp/a", RuntimeName: "claude", Branch: "feat/a"},
			{StoryID: "s-b", SessionName: "vxd-s-b", WorktreePath: "/tmp/b", RuntimeName: "gemini", Branch: "feat/b"},
		},
		Timestamp: time.Now(),
		PID:       42,
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	got, err := ReadCheckpoint(path)
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}

	if len(got.ActiveAgents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(got.ActiveAgents))
	}
	if got.ActiveAgents[1].RuntimeName != "gemini" {
		t.Errorf("expected gemini runtime, got %s", got.ActiveAgents[1].RuntimeName)
	}
}

func TestCheckpoint_PhaseConstants(t *testing.T) {
	// Verify phase constants are defined
	phases := []Phase{PhaseDispatching, PhaseMonitoring, PhaseMerging, PhaseCompleted}
	expected := []string{"dispatching", "monitoring", "merging", "completed"}
	for i, p := range phases {
		if string(p) != expected[i] {
			t.Errorf("phase %d: got %q, want %q", i, string(p), expected[i])
		}
	}
}
