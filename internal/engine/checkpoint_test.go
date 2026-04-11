package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := Checkpoint{
		ReqID:      "req-abc123",
		Phase:      PhaseMonitoring,
		WaveNumber: 2,
		ActiveAgents: []CheckpointAgent{
			{
				StoryID:      "abc12345-junior",
				SessionName:  "nxd-abc12345-junior",
				WorktreePath: "/tmp/nxd-abc12345-junior",
				RuntimeName:  "aider",
				Branch:       "feat/abc12345-junior",
			},
			{
				StoryID:      "abc12345-senior",
				SessionName:  "nxd-abc12345-senior",
				WorktreePath: "/tmp/nxd-abc12345-senior",
				RuntimeName:  "aider",
				Branch:       "feat/abc12345-senior",
			},
		},
		MergingStory: "",
		Timestamp:    time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC),
		PID:          12345,
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	got, err := ReadCheckpoint(path)
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}

	if got.ReqID != cp.ReqID {
		t.Errorf("ReqID: got %q, want %q", got.ReqID, cp.ReqID)
	}
	if got.Phase != cp.Phase {
		t.Errorf("Phase: got %q, want %q", got.Phase, cp.Phase)
	}
	if got.WaveNumber != cp.WaveNumber {
		t.Errorf("WaveNumber: got %d, want %d", got.WaveNumber, cp.WaveNumber)
	}
	if len(got.ActiveAgents) != 2 {
		t.Fatalf("ActiveAgents length: got %d, want 2", len(got.ActiveAgents))
	}
	if got.ActiveAgents[0].StoryID != "abc12345-junior" {
		t.Errorf("ActiveAgents[0].StoryID: got %q, want %q", got.ActiveAgents[0].StoryID, "abc12345-junior")
	}
	if got.ActiveAgents[0].SessionName != "nxd-abc12345-junior" {
		t.Errorf("ActiveAgents[0].SessionName: got %q, want %q", got.ActiveAgents[0].SessionName, "nxd-abc12345-junior")
	}
	if got.ActiveAgents[0].WorktreePath != "/tmp/nxd-abc12345-junior" {
		t.Errorf("ActiveAgents[0].WorktreePath: got %q, want %q", got.ActiveAgents[0].WorktreePath, "/tmp/nxd-abc12345-junior")
	}
	if got.ActiveAgents[0].RuntimeName != "aider" {
		t.Errorf("ActiveAgents[0].RuntimeName: got %q, want %q", got.ActiveAgents[0].RuntimeName, "aider")
	}
	if got.ActiveAgents[0].Branch != "feat/abc12345-junior" {
		t.Errorf("ActiveAgents[0].Branch: got %q, want %q", got.ActiveAgents[0].Branch, "feat/abc12345-junior")
	}
	if got.ActiveAgents[1].StoryID != "abc12345-senior" {
		t.Errorf("ActiveAgents[1].StoryID: got %q, want %q", got.ActiveAgents[1].StoryID, "abc12345-senior")
	}
	if got.MergingStory != "" {
		t.Errorf("MergingStory: got %q, want empty", got.MergingStory)
	}
	if !got.Timestamp.Equal(cp.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, cp.Timestamp)
	}
	if got.PID != 12345 {
		t.Errorf("PID: got %d, want 12345", got.PID)
	}
}

func TestWriteAndReadCheckpoint_WithMergingStory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := Checkpoint{
		ReqID:        "req-merge",
		Phase:        PhaseMerging,
		WaveNumber:   1,
		ActiveAgents: []CheckpointAgent{},
		MergingStory: "abc12345-junior",
		Timestamp:    time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC),
		PID:          54321,
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	got, err := ReadCheckpoint(path)
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}

	if got.MergingStory != "abc12345-junior" {
		t.Errorf("MergingStory: got %q, want %q", got.MergingStory, "abc12345-junior")
	}
	if got.Phase != PhaseMerging {
		t.Errorf("Phase: got %q, want %q", got.Phase, PhaseMerging)
	}
}

func TestReadCheckpoint_NoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	_, err := ReadCheckpoint(path)
	if err == nil {
		t.Fatal("expected error reading non-existent checkpoint, got nil")
	}
}

func TestCheckpoint_IsStale(t *testing.T) {
	threshold := 6 * time.Hour

	t.Run("stale_checkpoint", func(t *testing.T) {
		cp := Checkpoint{
			Timestamp: time.Now().Add(-7 * time.Hour),
		}
		if !cp.IsStale(threshold) {
			t.Error("7h-old checkpoint with 6h threshold should be stale")
		}
	})

	t.Run("fresh_checkpoint", func(t *testing.T) {
		cp := Checkpoint{
			Timestamp: time.Now().Add(-1 * time.Hour),
		}
		if cp.IsStale(threshold) {
			t.Error("1h-old checkpoint with 6h threshold should not be stale")
		}
	})

	t.Run("just_under_threshold", func(t *testing.T) {
		cp := Checkpoint{
			Timestamp: time.Now().Add(-5*time.Hour - 59*time.Minute),
		}
		if cp.IsStale(threshold) {
			t.Error("checkpoint just under 6h threshold should not be stale")
		}
	})
}

func TestClearCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := Checkpoint{
		ReqID:        "req-clear",
		Phase:        PhaseCompleted,
		WaveNumber:   1,
		ActiveAgents: []CheckpointAgent{},
		Timestamp:    time.Now(),
		PID:          os.Getpid(),
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("checkpoint file was not created")
	}

	ClearCheckpoint(path)

	// Read should fail after clear.
	_, err := ReadCheckpoint(path)
	if err == nil {
		t.Fatal("expected error reading cleared checkpoint, got nil")
	}

	// File should not exist.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("checkpoint file still exists after clear")
	}
}

func TestClearCheckpoint_SafeWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	// Should not panic when file doesn't exist.
	ClearCheckpoint(path)
}

func TestWriteCheckpoint_AtomicViaTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := Checkpoint{
		ReqID:        "req-atomic",
		Phase:        PhaseDispatching,
		WaveNumber:   1,
		ActiveAgents: []CheckpointAgent{},
		Timestamp:    time.Now(),
		PID:          os.Getpid(),
	}

	if err := WriteCheckpoint(path, cp); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	// Verify no .tmp file was left behind.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file was not cleaned up after atomic write")
	}

	// Verify the actual file is valid.
	got, err := ReadCheckpoint(path)
	if err != nil {
		t.Fatalf("ReadCheckpoint after atomic write: %v", err)
	}
	if got.ReqID != "req-atomic" {
		t.Errorf("ReqID: got %q, want %q", got.ReqID, "req-atomic")
	}
}

func TestPhaseConstants(t *testing.T) {
	phases := []Phase{PhaseDispatching, PhaseMonitoring, PhaseMerging, PhaseCompleted}
	expected := []string{"dispatching", "monitoring", "merging", "completed"}

	for i, phase := range phases {
		if string(phase) != expected[i] {
			t.Errorf("Phase %d: got %q, want %q", i, phase, expected[i])
		}
	}
}
