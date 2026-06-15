package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// eventsLenientEnv lets operators opt back into the legacy "silently
// skip corrupted JSONL lines" behaviour during emergency recovery
// (e.g. a half-written event from a crashed run). Default is strict
// because the projection store, retry counter, metrics aggregator, and
// resume logic all derive their truth from events.jsonl — silent
// corruption would let the system run on a degraded view of state
// without anyone noticing.
const eventsLenientEnv = "NXD_EVENTS_LENIENT"

// FileStore is a file-based append-only event store using JSONL format.
type FileStore struct {
	path     string
	file     *os.File
	mu       sync.RWMutex
	OnAppend func(Event) // optional callback invoked after each append
}

// NewFileStore creates a new FileStore that persists events to the given path.
func NewFileStore(path string) (*FileStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &FileStore{path: path, file: f}, nil
}

// Append writes a single event to the end of the JSONL file. If OnAppend is
// set, the callback is invoked after a successful write.
func (fs *FileStore) Append(event Event) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = fs.file.Write(append(data, '\n')); err != nil {
		return err
	}

	if fs.OnAppend != nil {
		fs.OnAppend(event)
	}
	return nil
}

// List reads all events from the file and returns those matching the filter.
func (fs *FileStore) List(filter EventFilter) ([]Event, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	return fs.readAndFilter(filter)
}

// Count returns the number of events matching the filter.
func (fs *FileStore) Count(filter EventFilter) (int, error) {
	events, err := fs.List(filter)
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

// Close closes the underlying file handle.
func (fs *FileStore) Close() error {
	return fs.file.Close()
}

func (fs *FileStore) readAndFilter(filter EventFilter) ([]Event, error) {
	f, err := os.Open(fs.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	lenient := os.Getenv(eventsLenientEnv) != ""

	var events []Event
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		// Skip wholly blank lines without surfacing them as corruption —
		// editors sometimes leave a trailing newline.
		if len(raw) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			if lenient {
				continue
			}
			return nil, fmt.Errorf(
				"events.jsonl line %d is corrupt: %w (set %s=1 to skip corrupt lines)",
				lineNo, err, eventsLenientEnv,
			)
		}
		if filter.Type != "" && evt.Type != filter.Type {
			continue
		}
		if filter.AgentID != "" && evt.AgentID != filter.AgentID {
			continue
		}
		if filter.StoryID != "" && evt.StoryID != filter.StoryID {
			continue
		}
		if !filter.After.IsZero() && !evt.Timestamp.After(filter.After) {
			continue
		}
		events = append(events, evt)
		if filter.Limit > 0 && len(events) >= filter.Limit {
			break
		}
	}
	return events, scanner.Err()
}
