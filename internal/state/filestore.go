package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
)

// eventsLenientEnv lets operators opt into "quarantine corrupted JSONL
// lines" behaviour during emergency recovery (e.g. a half-written event
// from a crashed run). Default is strict because the projection store,
// retry counter, metrics aggregator, and resume logic all derive their
// truth from events.jsonl — silent corruption would let the system run on
// a degraded view of state without anyone noticing. In lenient mode bad
// lines are moved to events.quarantine.jsonl (never silently dropped) and
// a log line names the count.
const eventsLenientEnv = "NXD_EVENTS_LENIENT"

// DefaultMaxEventBytes is the default cap on a single encoded event line.
// Larger payloads are truncated (longest string first) rather than dropped.
const DefaultMaxEventBytes = 1 << 20

// scanner buffer sizing: start at 1 MB, allow lines up to 16 MB. The
// bufio.Scanner default (64 KB) made a single large QA payload brick every
// command with "token too long".
const (
	scannerInitialBuf = 1 << 20
	scannerMaxLine    = 16 << 20
)

// FileStore is a file-based append-only event store using JSONL format.
type FileStore struct {
	path     string
	file     *os.File
	mu       sync.RWMutex
	OnAppend func(Event) // optional callback invoked after each append

	maxEventBytes int
	fsync         bool

	// tailChecked is set once the first Append has verified the file does
	// not end in a torn (newline-less) fragment left by a crashed writer.
	tailChecked bool
	tornLogged  bool
}

// FileStoreOption customises a FileStore at construction time.
type FileStoreOption func(*FileStore)

// WithMaxEventBytes caps the encoded size of a single event line. Payload
// strings are truncated longest-first until the line fits; n <= 0 restores
// the default.
func WithMaxEventBytes(n int) FileStoreOption {
	return func(fs *FileStore) {
		if n > 0 {
			fs.maxEventBytes = n
		}
	}
}

// WithFsync controls whether every Append is followed by an fsync. Default
// true: the event log is the source of truth and a lost tail after a crash
// desyncs every derived view.
func WithFsync(enabled bool) FileStoreOption {
	return func(fs *FileStore) { fs.fsync = enabled }
}

// NewFileStore creates a new FileStore that persists events to the given path.
func NewFileStore(path string, opts ...FileStoreOption) (*FileStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	fs := &FileStore{path: path, file: f, maxEventBytes: DefaultMaxEventBytes, fsync: true}
	for _, opt := range opts {
		opt(fs)
	}
	return fs, nil
}

// Append writes a single event to the end of the JSONL file. If OnAppend is
// set, the callback is invoked after a successful write.
//
// Oversized payloads are truncated (see TruncatePayload) so the line fits
// maxEventBytes; the event itself is never dropped. When fsync is enabled the
// write is flushed to stable storage before returning.
func (fs *FileStore) Append(event Event) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if !fs.tailChecked {
		fs.tailChecked = true
		if err := fs.repairTornTailLocked(); err != nil {
			return fmt.Errorf("repair torn tail before append: %w", err)
		}
	}

	data, err := fs.encode(event)
	if err != nil {
		return err
	}
	if _, err = fs.file.Write(append(data, '\n')); err != nil {
		return err
	}
	if fs.fsync {
		if err := fs.file.Sync(); err != nil {
			return fmt.Errorf("fsync events log: %w", err)
		}
	}

	if fs.OnAppend != nil {
		fs.OnAppend(event)
	}
	return nil
}

// encode marshals the event, truncating payload strings when the encoded
// line would exceed maxEventBytes.
func (fs *FileStore) encode(event Event) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(data)+1 <= fs.maxEventBytes || len(event.Payload) == 0 {
		return data, nil
	}
	// Budget for the payload = cap minus the envelope. Payload is stored as
	// base64 (json []byte), so the raw JSON budget is 3/4 of the encoded room.
	envelope := len(data) - base64Len(len(event.Payload))
	room := fs.maxEventBytes - 1 - envelope
	budget := room * 3 / 4
	if budget < 64 {
		budget = 64
	}
	payload := DecodePayload(event.Payload)
	trimmed, truncated := TruncatePayload(payload, budget)
	if !truncated {
		return data, nil
	}
	enc, err := json.Marshal(trimmed)
	if err != nil {
		return data, nil // fall back to the untruncated line; never drop
	}
	event.Payload = enc
	return json.Marshal(event)
}

// base64Len returns the encoded length (with quotes) of n raw bytes.
func base64Len(n int) int {
	return (n+2)/3*4 + 2
}

// List reads all events from the file and returns those matching the filter.
func (fs *FileStore) List(filter EventFilter) ([]Event, error) {
	fs.mu.RLock()
	events, bad, err := fs.readAndFilter(filter)
	fs.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if len(bad) > 0 {
		// Lenient mode found malformed lines: move them out of the live log
		// so they are neither silently lost nor re-quarantined on every read.
		fs.mu.Lock()
		qerr := fs.quarantineLinesLocked(bad)
		fs.mu.Unlock()
		if qerr != nil {
			return nil, qerr
		}
	}
	return events, nil
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

// badLine is a malformed JSONL line found while scanning, kept for
// quarantine. lineNo is 1-based.
type badLine struct {
	lineNo int
	raw    []byte
}

// readAndFilter scans the log, applying filter. It returns the matching
// events and — in lenient mode only — the malformed lines it skipped so the
// caller can quarantine them. A torn final line (no trailing newline) is
// skipped in both modes; malformed lines elsewhere are errors in strict mode.
func (fs *FileStore) readAndFilter(filter EventFilter) ([]Event, []badLine, error) {
	f, err := os.Open(fs.path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	lenient := os.Getenv(eventsLenientEnv) != ""
	tornTail := hasTornTail(f)

	var events []Event
	var bad []badLine

	// A malformed line is held as "pending" until we know whether anything
	// follows it: only the very last line of a newline-less file counts as a
	// torn write. Everything else is corruption.
	var pending *badLine
	var pendingErr error
	flush := func() error {
		if pending == nil {
			return nil
		}
		b := *pending
		pending, pendingErr = nil, nil
		if lenient {
			bad = append(bad, b)
			return nil
		}
		return fmt.Errorf(
			"events.jsonl line %d is corrupt: %w (set %s=1 to quarantine corrupt lines, or run `nxd state repair`)",
			b.lineNo, pendingErr, eventsLenientEnv,
		)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLine)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		// Skip wholly blank lines without surfacing them as corruption —
		// editors sometimes leave a trailing newline.
		if len(raw) == 0 {
			continue
		}
		if err := flush(); err != nil {
			return nil, nil, err
		}
		var evt Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			pending = &badLine{lineNo: lineNo, raw: append([]byte(nil), raw...)}
			pendingErr = err
			continue
		}
		if !matchesFilter(evt, filter) {
			continue
		}
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if pending != nil && tornTail {
		fs.logTornOnce(pending.lineNo)
		pending = nil
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	// Limit returns the most recent N matching events (the tail of the log),
	// preserving chronological order. Every caller — the web "Last 50 events"
	// snapshot, the dashboard activity feed, the Hub delta push — wants recent
	// activity, so we must keep the LAST N, not truncate from the front.
	if filter.Limit > 0 && len(events) > filter.Limit {
		events = events[len(events)-filter.Limit:]
	}
	return events, bad, nil
}

// matchesFilter reports whether evt satisfies every set field of filter.
func matchesFilter(evt Event, filter EventFilter) bool {
	if filter.Type != "" && evt.Type != filter.Type {
		return false
	}
	if filter.AgentID != "" && evt.AgentID != filter.AgentID {
		return false
	}
	if filter.StoryID != "" && evt.StoryID != filter.StoryID {
		return false
	}
	if !filter.After.IsZero() && !evt.Timestamp.After(filter.After) {
		return false
	}
	return true
}

// logTornOnce logs the torn-tail skip a single time per FileStore so a
// dashboard polling every second does not flood the log.
func (fs *FileStore) logTornOnce(lineNo int) {
	if fs.tornLogged {
		return
	}
	fs.tornLogged = true
	log.Printf("[state] events.jsonl line %d is a torn write (no trailing newline); skipping it. "+
		"It will be quarantined on the next append; run `nxd state check` for details", lineNo)
}
