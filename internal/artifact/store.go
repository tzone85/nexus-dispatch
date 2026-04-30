package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// Type identifies the kind of artifact stored for a story.
type Type string

const (
	TypeLaunchConfig Type = "launch_config"
	TypeTraceEvents  Type = "trace_events"
	TypeGitDiff      Type = "git_diff"
	TypeQAResult     Type = "qa_result"
	TypeReviewResult Type = "review_result"
	TypeRawLog       Type = "raw_log"
)

// LaunchConfig captures the exact context an agent was spawned with.
type LaunchConfig struct {
	StoryID   string            `json:"story_id"`
	Runtime   string            `json:"runtime"`
	Model     string            `json:"model"`
	Prompt    string            `json:"prompt"`
	WaveBrief string            `json:"wave_brief,omitempty"`
	EnvVars   map[string]string `json:"env_vars,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// Store manages per-story artifact directories on the filesystem.
// All methods are safe for concurrent use.
type Store struct {
	baseDir string
	mu      sync.Mutex
}

// NewStore creates a Store rooted at baseDir (typically {stateDir}/artifacts).
// The directory is created if it does not exist.
func NewStore(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact base dir: %w", err)
	}
	return &Store{baseDir: baseDir}, nil
}

// storyDir validates storyID and returns the safe per-story directory.
func (s *Store) storyDir(storyID string) (string, error) {
	if !sanitize.ValidIdentifier(storyID) {
		return "", fmt.Errorf("invalid story id: %q", storyID)
	}
	return sanitize.SafeJoin(s.baseDir, storyID)
}

// Init creates the artifact directory for a story.
func (s *Store) Init(storyID string) error {
	dir, err := s.storyDir(storyID)
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// Write stores a typed artifact as JSON for the given story.
func (s *Store) Write(storyID string, artifactType Type, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.storyDir(storyID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifact dir: %w", err)
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}

	path := filepath.Join(dir, string(artifactType)+".json")
	return os.WriteFile(path, content, 0o644)
}

// WriteRaw stores a raw text artifact (e.g. diff, log) for the given story.
func (s *Store) WriteRaw(storyID string, artifactType Type, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.storyDir(storyID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifact dir: %w", err)
	}

	ext := ".txt"
	if artifactType == TypeGitDiff {
		ext = ".patch"
	}
	path := filepath.Join(dir, string(artifactType)+ext)
	return os.WriteFile(path, []byte(content), 0o644)
}

// Append adds a line to a JSONL artifact file (e.g. trace events).
func (s *Store) Append(storyID string, artifactType Type, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.storyDir(storyID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifact dir: %w", err)
	}

	line, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal trace entry: %w", err)
	}
	line = append(line, '\n')

	path := filepath.Join(dir, string(artifactType)+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open trace file: %w", err)
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}

// Read returns the raw contents of a typed artifact.
// Both storyID and filename are validated to prevent path traversal.
func (s *Store) Read(storyID string, filename string) ([]byte, error) {
	dir, err := s.storyDir(storyID)
	if err != nil {
		return nil, err
	}
	path, err := sanitize.SafeJoin(dir, filename)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// List returns the filenames of all artifacts stored for a story.
func (s *Store) List(storyID string) ([]string, error) {
	dir, err := s.storyDir(storyID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
