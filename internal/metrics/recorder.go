package metrics

import (
	"bufio"
	"encoding/json"
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
}

// Recorder persists MetricEntry records as JSONL to a file.
type Recorder struct {
	path string
	mu   sync.Mutex
}

// NewRecorder creates a Recorder that writes to the given file path.
func NewRecorder(path string) *Recorder {
	return &Recorder{path: path}
}

// Record appends a MetricEntry as a JSON line to the file.
func (r *Recorder) Record(entry MetricEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadAll reads all MetricEntry records from the JSONL file.
// Returns nil, nil if the file does not exist.
func (r *Recorder) ReadAll() ([]MetricEntry, error) {
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
	for scanner.Scan() {
		var entry MetricEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}
