package state

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MalformedLine describes one unparseable line found by Check.
type MalformedLine struct {
	Line    int    `json:"line"`
	Error   string `json:"error"`
	Snippet string `json:"snippet"`
}

// Report is the result of Check: a read-only health summary of an events log.
type Report struct {
	Path          string          `json:"path"`
	Lines         int             `json:"lines"`
	Valid         int             `json:"valid"`
	Torn          bool            `json:"torn"`
	Malformed     []MalformedLine `json:"malformed"`
	SizeBytes     int64           `json:"size_bytes"`
	LastEventTime time.Time       `json:"last_event_time"`
}

// Healthy reports whether the log has no malformed lines and no torn tail.
func (r Report) Healthy() bool { return !r.Torn && len(r.Malformed) == 0 }

// Check scans the events log at path without modifying it. A torn final
// line (no trailing newline) is reported via Torn rather than Malformed;
// every other unparseable line is listed in Malformed with its line number.
func Check(path string) (Report, error) {
	rep := Report{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return rep, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return rep, err
	}
	rep.SizeBytes = fi.Size()
	torn := hasTornTail(f)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLine)
	var pending *MalformedLine
	lineNo := 0
	for scanner.Scan() {
		raw := scanner.Bytes()
		lineNo++
		if len(raw) == 0 {
			continue
		}
		if pending != nil {
			rep.Malformed = append(rep.Malformed, *pending)
			pending = nil
		}
		rep.Lines++
		var evt Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			pending = &MalformedLine{Line: lineNo, Error: err.Error(), Snippet: snippet(raw)}
			continue
		}
		rep.Valid++
		if evt.Timestamp.After(rep.LastEventTime) {
			rep.LastEventTime = evt.Timestamp
		}
	}
	if err := scanner.Err(); err != nil {
		return rep, err
	}
	if pending != nil {
		if torn {
			rep.Torn = true
		} else {
			rep.Malformed = append(rep.Malformed, *pending)
		}
	}
	return rep, nil
}

// snippet returns the first 60 bytes of raw for human display.
func snippet(raw []byte) string {
	const n = 60
	if len(raw) <= n {
		return string(raw)
	}
	return string(raw[:n]) + "…"
}

// Repair moves every malformed line and any torn tail from the log at path
// into the sibling quarantine file, rewrites the log atomically (temp file +
// rename) and leaves a "<path>.bak" copy of the original. It returns the
// number of lines moved. A healthy log is left untouched (0, nil).
//
// Repair must not run while a FileStore holds path open for appends: the
// rename changes the inode under the writer. The CLI takes the pipeline
// lock before calling it.
func Repair(path string) (int, error) {
	rep, err := Check(path)
	if err != nil {
		return 0, err
	}
	if rep.Healthy() {
		return 0, nil
	}
	if err := copyFile(path, path+".bak"); err != nil {
		return 0, fmt.Errorf("write backup: %w", err)
	}
	moved := 0
	if rep.Torn {
		offset, fragment, err := tornFragment(path)
		if err != nil {
			return 0, err
		}
		if err := appendQuarantine(path, [][]byte{fragment}); err != nil {
			return 0, err
		}
		if err := os.Truncate(path, offset); err != nil {
			return 0, fmt.Errorf("truncate torn tail: %w", err)
		}
		moved++
	}
	if len(rep.Malformed) == 0 {
		return moved, nil
	}
	drop := make(map[int]struct{}, len(rep.Malformed))
	for _, m := range rep.Malformed {
		drop[m.Line] = struct{}{}
	}
	raws, err := readLines(path, drop)
	if err != nil {
		return moved, err
	}
	if err := appendQuarantine(path, raws); err != nil {
		return moved, err
	}
	if _, err := rewriteWithout(path, drop, false); err != nil {
		return moved, err
	}
	return moved + len(raws), nil
}

// readLines returns the raw content of the given 1-based line numbers.
func readLines(path string, want map[int]struct{}) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLine)
	var out [][]byte
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if _, ok := want[lineNo]; ok {
			out = append(out, append([]byte(nil), scanner.Bytes()...))
		}
	}
	return out, scanner.Err()
}

// CompactResult summarises a Compact run.
type CompactResult struct {
	Removed     int    `json:"removed"`
	Kept        int    `json:"kept"`
	ArchivePath string `json:"archive_path,omitempty"`
}

// compactable lists the high-volume, purely informational event types that
// Compact is allowed to drop once their requirement has completed. Nothing
// else is ever removed: every other event feeds the projection or an audit
// trail.
var compactable = map[EventType]bool{
	EventStoryProgress:   true,
	EventAgentCheckpoint: true,
}

// Compact removes STORY_PROGRESS and AGENT_CHECKPOINT events belonging to
// stories of requirements that have reached REQ_COMPLETED, writing the
// removed lines to "events.archive-<timestamp>.jsonl" next to the log and
// leaving a "<path>.bak" copy. The log is rewritten atomically. It refuses
// to run on a log with malformed lines or a torn tail (run Repair first).
//
// Requirement membership is derived from the log itself (STORY_CREATED
// carries req_id; REQ_COMPLETED carries id), so Compact needs no projection
// and is safe to run without a database.
func Compact(path string, now time.Time) (CompactResult, error) {
	rep, err := Check(path)
	if err != nil {
		return CompactResult{}, err
	}
	if !rep.Healthy() {
		return CompactResult{}, fmt.Errorf("log has %d malformed line(s) (torn=%v); run `nxd state repair` first",
			len(rep.Malformed), rep.Torn)
	}

	events, err := readAllEvents(path)
	if err != nil {
		return CompactResult{}, err
	}
	storyReq := make(map[string]string)
	completed := make(map[string]bool)
	for _, e := range events {
		p := DecodePayload(e.Payload)
		switch e.Type {
		case EventStoryCreated:
			if id, rq := payloadStr(p, "id"), payloadStr(p, "req_id"); id != "" && rq != "" {
				storyReq[id] = rq
			}
		case EventReqCompleted:
			if id := payloadStr(p, "id"); id != "" {
				completed[id] = true
			}
		}
	}

	drop := make(map[int]struct{})
	var archived [][]byte
	for i, e := range events {
		if !compactable[e.Type] || e.StoryID == "" || !completed[storyReq[e.StoryID]] {
			continue
		}
		drop[i+1] = struct{}{}
		line, _ := json.Marshal(e)
		archived = append(archived, line)
	}
	if len(drop) == 0 {
		return CompactResult{Kept: len(events)}, nil
	}

	archivePath := filepath.Join(filepath.Dir(path),
		fmt.Sprintf("events.archive-%s.jsonl", now.UTC().Format("20060102T150405Z")))
	af, err := os.OpenFile(archivePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return CompactResult{}, fmt.Errorf("open archive: %w", err)
	}
	for _, l := range archived {
		if _, err := af.Write(append(l, '\n')); err != nil {
			_ = af.Close()
			return CompactResult{}, fmt.Errorf("write archive: %w", err)
		}
	}
	if err := af.Sync(); err != nil {
		_ = af.Close()
		return CompactResult{}, err
	}
	if err := af.Close(); err != nil {
		return CompactResult{}, err
	}

	kept, err := rewriteWithout(path, drop, true)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{Removed: len(drop), Kept: kept, ArchivePath: archivePath}, nil
}

// readAllEvents parses every line of a healthy log in order. Line i of the
// file is events[i-1]; blank lines are not expected (Check has run).
func readAllEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLine)
	var out []Event
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", len(out)+1, err)
		}
		out = append(out, e)
	}
	return out, scanner.Err()
}

// LockFunc acquires the pipeline lock and returns a release function. It is
// injected (rather than imported) because the lock lives in internal/engine,
// which depends on this package.
type LockFunc func() (release func(), err error)

// Rebuild replays the event log into the projection under the pipeline
// lock so it can never race a running pipeline's own writes. The lock is
// released before returning.
func Rebuild(ctx context.Context, es EventStore, proj *SQLiteStore, acquire LockFunc) error {
	if acquire != nil {
		release, err := acquire()
		if err != nil {
			return fmt.Errorf("rebuild: %w", err)
		}
		defer release()
	}
	return proj.RebuildFrom(ctx, es)
}
