package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Phase represents a pipeline stage for crash recovery checkpoints.
type Phase string

const (
	PhaseDispatching Phase = "dispatching"
	PhaseMonitoring  Phase = "monitoring"
	PhaseMerging     Phase = "merging"
	PhaseCompleted   Phase = "completed"
)

// CheckpointAgent records the state of an active agent at checkpoint time.
type CheckpointAgent struct {
	StoryID      string `json:"story_id"`
	SessionName  string `json:"session_name"`
	WorktreePath string `json:"worktree_path"`
	RuntimeName  string `json:"runtime_name"`
	Branch       string `json:"branch"`
}

// Checkpoint records the pipeline state at a phase transition, enabling
// crash recovery when the process dies mid-run.
type Checkpoint struct {
	ReqID        string            `json:"req_id"`
	Phase        Phase             `json:"phase"`
	WaveNumber   int               `json:"wave_number"`
	ActiveAgents []CheckpointAgent `json:"active_agents"`
	MergingStory string            `json:"merging_story,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	PID          int               `json:"pid"`
}

// WriteCheckpoint atomically writes the checkpoint to disk. It first writes
// to a temporary file, then renames it into place to prevent partial writes.
func WriteCheckpoint(path string, cp Checkpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write checkpoint tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Best-effort cleanup of the temp file.
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename checkpoint: %w", err)
	}

	return nil
}

// ReadCheckpoint reads and unmarshals a checkpoint from disk.
func ReadCheckpoint(path string) (Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	return cp, nil
}

// ClearCheckpoint removes the checkpoint file. It is safe to call when the
// file does not exist.
func ClearCheckpoint(path string) {
	_ = os.Remove(path)
}

// IsStale returns true if the checkpoint is older than the given threshold.
func (cp Checkpoint) IsStale(threshold time.Duration) bool {
	return time.Since(cp.Timestamp) > threshold
}
