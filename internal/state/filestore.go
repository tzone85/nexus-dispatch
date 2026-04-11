package state

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

// FileStore is a file-based append-only event store using JSONL format.
type FileStore struct {
	path     string
	file     *os.File
	mu       sync.RWMutex
	OnAppend func(Event) // optional callback invoked after each append
}

// NewFileStore creates a new FileStore that persists events to the given path.
func NewFileStore(path string) (*FileStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
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

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var evt Event
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
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
