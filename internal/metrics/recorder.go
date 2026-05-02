package metrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// MetricEntry represents a single LLM call metric record.
type MetricEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	ReqID      string    `json:"req_id"`
	StoryID    string    `json:"story_id,omitempty"`
	Phase      string    `json:"phase"`
	Role       string    `json:"role,omitempty"`
	Model      string    `json:"model"`
	TokensIn   int       `json:"tokens_in"`
	TokensOut  int       `json:"tokens_out"`
	DurationMs int64     `json:"duration_ms"`
	Success    bool      `json:"success"`
	Escalated  bool      `json:"escalated,omitempty"`
	// Tier records the agent tier (0=junior, 1=senior, 2=manager, 3=tech_lead).
	// Used by the reporter to surface escalation cost. Optional — older records
	// without this field still parse (zero value = junior).
	Tier int `json:"tier,omitempty"`
	// Stage labels the pipeline phase that produced the metric (planner /
	// dispatcher / executor / reviewer / qa / merger). Distinct from Phase
	// which is more granular ("classify", "investigate", etc).
	Stage string `json:"stage,omitempty"`
}

// Recorder persists MetricEntry records as JSONL to a file.
//
// Performance note (B1.5 quick win): the file handle is opened lazily on
// first Record and kept open for the lifetime of the Recorder; previously
// every Record() did an OpenFile + Write + Close, which dominated the
// per-iteration cost of the Gemma loop. A bufio.Writer flushes after each
// line so JSONL stays append-correct even under crash, but amortizes the
// underlying syscall.
type Recorder struct {
	path string
	mu   sync.Mutex

	// Initialized lazily by ensureOpen under mu.
	f  *os.File
	bw *bufio.Writer
}

// NewRecorder creates a Recorder that writes to the given file path.
func NewRecorder(path string) *Recorder {
	return &Recorder{path: path}
}

// ensureOpen lazily opens the underlying file. Caller must hold r.mu.
func (r *Recorder) ensureOpen() error {
	if r.f != nil {
		return nil
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.f = f
	r.bw = bufio.NewWriterSize(f, 4096)
	return nil
}

// Record appends a MetricEntry as a JSON line to the file.
func (r *Recorder) Record(entry MetricEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureOpen(); err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := r.bw.Write(data); err != nil {
		return err
	}
	if err := r.bw.WriteByte('\n'); err != nil {
		return err
	}
	// Flush per-line so a crash loses at most one entry, not the whole
	// buffered tail. The 4 KiB buffer still merges adjacent writes from
	// the same goroutine.
	return r.bw.Flush()
}

// Close releases the underlying file handle. Safe to call multiple times.
// After Close the Recorder is unusable; callers should drop the reference.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	flushErr := r.bw.Flush()
	closeErr := r.f.Close()
	r.f, r.bw = nil, nil
	if flushErr != nil {
		return fmt.Errorf("flush: %w", flushErr)
	}
	return closeErr
}

// ReadAll reads all MetricEntry records from the JSONL file.
// Returns nil, nil if the file does not exist.
//
// ReadAll opens its own read-only handle and does not interfere with the
// writer; concurrent Record calls remain safe.
func (r *Recorder) ReadAll() ([]MetricEntry, error) {
	// Flush any pending buffered writes so a reader sees the latest record.
	r.mu.Lock()
	if r.bw != nil {
		_ = r.bw.Flush()
	}
	r.mu.Unlock()

	f, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []MetricEntry
	scanner := bufio.NewScanner(f)
	// Allow long lines (large prompts truncated upstream, but be forgiving).
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var entry MetricEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return entries, err
	}
	return entries, nil
}
