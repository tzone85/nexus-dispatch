package engine

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"
)

// TraceEventKind classifies a trace event.
type TraceEventKind string

const (
	TraceToolCall   TraceEventKind = "tool_call"
	TraceFileEdit   TraceEventKind = "file_edit"
	TraceFileCreate TraceEventKind = "file_create"
	TraceCommand    TraceEventKind = "command"
	TraceError      TraceEventKind = "error"
	TraceTest       TraceEventKind = "test"
	TraceCommit     TraceEventKind = "commit"
	TraceProgress   TraceEventKind = "progress"
)

// TraceEvent is a single normalized event extracted from agent output.
type TraceEvent struct {
	Kind      TraceEventKind `json:"kind"`
	Timestamp time.Time      `json:"timestamp"`
	Content   string         `json:"content"`
	File      string         `json:"file,omitempty"`
	Line      int            `json:"line,omitempty"`
}

// Patterns for detecting events in Claude Code terminal output.
var (
	traceToolCallRe   = regexp.MustCompile(`(?i)(?:Read|Write|Edit|Bash|Grep|Glob|Agent)\s*[(\[]`)
	traceFileEditRe   = regexp.MustCompile(`(?:Edited|Updated|Modified)\s+(.+\.(?:go|py|ts|js|tsx|jsx|rs|rb|java|c|cpp|h))`)
	traceFileCreateRe = regexp.MustCompile(`(?:Created|Wrote)\s+(.+\.(?:go|py|ts|js|tsx|jsx|rs|rb|java|c|cpp|h))`)
	traceBashCmdRe    = regexp.MustCompile(`(?:^|\s)\$\s+(.+)`)
	traceErrorRe      = regexp.MustCompile(`(?i)(?:error|FAIL|panic|fatal|undefined|cannot find)`)
	traceTestRe       = regexp.MustCompile(`(?:PASS|FAIL|ok\s+\S+|---\s+(?:PASS|FAIL))`)
	traceCommitRe     = regexp.MustCompile(`(?:\[[\w/]+\s+[a-f0-9]+\]|git commit)`)
)

// ParseTraceFile reads an agent log file and extracts normalized trace events.
func ParseTraceFile(path string) ([]TraceEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []TraceEvent
	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for long lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if evt, ok := classifyLine(line); ok {
			events = append(events, evt)
		}
	}

	return events, scanner.Err()
}

// ParseTraceString parses trace events from a string (useful for testing).
func ParseTraceString(content string) []TraceEvent {
	var events []TraceEvent
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		if evt, ok := classifyLine(line); ok {
			events = append(events, evt)
		}
	}
	return events
}

// TraceSummary summarizes a list of trace events.
type TraceSummary struct {
	ToolCalls   int `json:"tool_calls"`
	FileEdits   int `json:"file_edits"`
	FileCreates int `json:"file_creates"`
	Commands    int `json:"commands"`
	Errors      int `json:"errors"`
	Tests       int `json:"tests"`
	Commits     int `json:"commits"`
}

// Summarize aggregates trace events into a summary.
func Summarize(events []TraceEvent) TraceSummary {
	var s TraceSummary
	for _, e := range events {
		switch e.Kind {
		case TraceToolCall:
			s.ToolCalls++
		case TraceFileEdit:
			s.FileEdits++
		case TraceFileCreate:
			s.FileCreates++
		case TraceCommand:
			s.Commands++
		case TraceError:
			s.Errors++
		case TraceTest:
			s.Tests++
		case TraceCommit:
			s.Commits++
		}
	}
	return s
}

// classifyLine matches a single line against known patterns and returns
// a TraceEvent if recognised. More specific patterns are checked first.
func classifyLine(line string) (TraceEvent, bool) {
	trimmed := strings.TrimSpace(line)

	// Order matters — more specific patterns first.
	if traceCommitRe.MatchString(trimmed) {
		return TraceEvent{Kind: TraceCommit, Content: trimmed, Timestamp: time.Now().UTC()}, true
	}
	if traceTestRe.MatchString(trimmed) {
		return TraceEvent{Kind: TraceTest, Content: trimmed, Timestamp: time.Now().UTC()}, true
	}
	if m := traceFileCreateRe.FindStringSubmatch(trimmed); len(m) > 1 {
		return TraceEvent{Kind: TraceFileCreate, Content: trimmed, File: m[1], Timestamp: time.Now().UTC()}, true
	}
	if m := traceFileEditRe.FindStringSubmatch(trimmed); len(m) > 1 {
		return TraceEvent{Kind: TraceFileEdit, Content: trimmed, File: m[1], Timestamp: time.Now().UTC()}, true
	}
	if traceToolCallRe.MatchString(trimmed) {
		return TraceEvent{Kind: TraceToolCall, Content: trimmed, Timestamp: time.Now().UTC()}, true
	}
	if traceErrorRe.MatchString(trimmed) {
		// Skip false positives in import paths, test names, and filenames.
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "error_test") || strings.Contains(lower, "errors.go") ||
			strings.Contains(lower, "errorhandl") || strings.Contains(lower, "testerror") {
			return TraceEvent{}, false
		}
		return TraceEvent{Kind: TraceError, Content: trimmed, Timestamp: time.Now().UTC()}, true
	}

	return TraceEvent{}, false
}
