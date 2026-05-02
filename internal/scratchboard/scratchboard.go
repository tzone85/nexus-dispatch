package scratchboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxReadEntries is the default limit on entries returned by Read.
const MaxReadEntries = 20

// Entry represents a single knowledge entry written by an agent.
type Entry struct {
	AgentID   string    `json:"agent_id"`
	StoryID   string    `json:"story_id"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Scratchboard is a thread-safe shared knowledge store scoped to a single
// requirement run. Agents can write discoveries and read context from other
// agents. Backed by a JSONL file.
type Scratchboard struct {
	path string
	mu   sync.RWMutex
}

// New creates a Scratchboard backed by a JSONL file at the given path.
// The parent directory is created if it does not exist.
func New(path string) (*Scratchboard, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create scratchboard dir: %w", err)
	}
	return &Scratchboard{path: path}, nil
}

// Write appends an entry to the scratchboard.
func (s *Scratchboard) Write(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open scratchboard: %w", err)
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}

// Read returns the most recent entries (up to limit). If category is non-empty,
// only entries matching that category are returned. Returns newest first.
//
// Performance note (B1.5): single-pass reverse iteration over file lines.
// Previous version walked forward, allocated all entries, then manually
// reversed; this walks backward and stops once limit is hit. For a hot
// scratchboard with thousands of entries that's a 100x reduction in
// allocated entries on each call.
func (s *Scratchboard) Read(category string, limit int) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = MaxReadEntries
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read scratchboard: %w", err)
	}

	lines := splitLines(data)
	out := make([]Entry, 0, limit)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		line := lines[i]
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if category != "" && e.Category != category {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Snapshot returns all entries formatted as a markdown string suitable for
// inclusion in agent prompts.
func (s *Scratchboard) Snapshot(limit int) string {
	entries, err := s.Read("", limit)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var b []byte
	b = append(b, "## Shared Discoveries (from parallel agents)\n\n"...)
	for _, e := range entries {
		b = append(b, fmt.Sprintf("- [%s/%s] %s\n", e.StoryID, e.Category, e.Content)...)
	}
	return string(b)
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
