# MemPalace, Context Sharing, QA Feedback, Observability & Agent Intelligence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add MemPalace semantic memory, agent context sharing, QA feedback loops, observability/metrics, smarter retry, live watch, parallel awareness, and convention detection — transforming NXD from isolated-agent execution to a context-aware, observable, self-improving system.

**Architecture:** MemPalace (Python) is accessed via a JSON bridge script called from Go via subprocess. A new `internal/memory/` package wraps the bridge. A new `internal/metrics/` package tracks token usage and timing via a Client wrapper. Pipeline changes in monitor.go wire context sharing (mine diffs, query prior work) and QA feedback (error output fed back to agents). New CLI commands (`nxd metrics`, `nxd watch`) expose observability.

**Tech Stack:** Go 1.23+, Python 3.9+ (MemPalace via pip), existing llm.Client interface, state.EventStore/SQLiteStore

**Spec:** `docs/superpowers/specs/2026-04-10-mempalace-context-observability-design.md`

---

## Phase 1: MemPalace Integration

### Task 1: MemPalace Python Bridge

**Files:**
- Create: `scripts/mempalace_bridge.py`

- [ ] **Step 1: Write the bridge script**

```python
#!/usr/bin/env python3
"""MemPalace bridge for NXD. Returns JSON for all operations."""
import argparse
import json
import sys

def cmd_health(args):
    try:
        import mempalace
        return {"status": "ok", "version": mempalace.__version__}
    except ImportError:
        return {"status": "error", "message": "mempalace not installed. Run: pip install mempalace"}

def cmd_search(args):
    try:
        from mempalace.searcher import search_memories
        raw = search_memories(
            args.query,
            palace_path=args.palace or None,
            wing=args.wing or None,
        )
        results = []
        for r in raw.get("results", []):
            entry = {
                "text": r.get("text", ""),
                "wing": r.get("wing", ""),
                "room": r.get("room", ""),
                "similarity": r.get("similarity", 0.0),
            }
            results.append(entry)
        # Filter by room if specified
        if args.room:
            results = [r for r in results if r["room"] == args.room]
        # Limit results
        results = results[:args.results]
        return {"status": "ok", "results": results}
    except ImportError:
        return {"status": "error", "message": "mempalace not installed"}
    except Exception as e:
        return {"status": "error", "message": str(e)}

def cmd_mine(args):
    try:
        from mempalace.miner import mine_text
        mine_text(
            text=args.text,
            wing=args.wing,
            room=args.room,
            palace_path=args.palace or None,
        )
        return {"status": "ok"}
    except ImportError:
        return {"status": "error", "message": "mempalace not installed"}
    except Exception as e:
        return {"status": "error", "message": str(e)}

def cmd_mine_meta(args):
    try:
        from mempalace.miner import mine_text
        mine_text(
            text=args.text,
            wing="nxd_meta",
            room="learnings",
            palace_path=args.palace or None,
        )
        return {"status": "ok"}
    except ImportError:
        return {"status": "error", "message": "mempalace not installed"}
    except Exception as e:
        return {"status": "error", "message": str(e)}

def cmd_wakeup(args):
    try:
        from mempalace.context import wake_up
        context = wake_up(wing=args.wing, palace_path=args.palace or None)
        return {"status": "ok", "context": context}
    except ImportError:
        return {"status": "error", "message": "mempalace not installed"}
    except Exception as e:
        return {"status": "error", "message": str(e)}

def main():
    parser = argparse.ArgumentParser(description="MemPalace bridge for NXD")
    parser.add_argument("--palace", default="", help="Palace path override")
    sub = parser.add_subparsers(dest="command")

    sub.add_parser("health")

    s = sub.add_parser("search")
    s.add_argument("--query", required=True)
    s.add_argument("--wing", default="")
    s.add_argument("--room", default="")
    s.add_argument("--results", type=int, default=5)

    m = sub.add_parser("mine")
    m.add_argument("--wing", required=True)
    m.add_argument("--room", required=True)
    m.add_argument("--text", required=True)

    mm = sub.add_parser("mine-meta")
    mm.add_argument("--text", required=True)

    w = sub.add_parser("wake-up")
    w.add_argument("--wing", required=True)

    args = parser.parse_args()
    handlers = {
        "health": cmd_health,
        "search": cmd_search,
        "mine": cmd_mine,
        "mine-meta": cmd_mine_meta,
        "wake-up": cmd_wakeup,
    }
    handler = handlers.get(args.command)
    if not handler:
        print(json.dumps({"status": "error", "message": f"unknown command: {args.command}"}))
        sys.exit(1)

    result = handler(args)
    print(json.dumps(result))

if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Verify it runs**

Run: `python3 scripts/mempalace_bridge.py health`
Expected: `{"status": "ok", "version": "..."}` or `{"status": "error", "message": "mempalace not installed"}`

- [ ] **Step 3: Commit**

```bash
git add scripts/mempalace_bridge.py
git commit -m "feat: add MemPalace Python bridge script"
```

---

### Task 2: Go MemPalace Client

**Files:**
- Create: `internal/memory/mempalace.go`
- Create: `internal/memory/mempalace_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/memory/mempalace_test.go
package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewMemPalace_FindsBridge(t *testing.T) {
	mp := NewMemPalace()
	if mp.bridgePath == "" {
		t.Error("expected bridge path to be found")
	}
}

func TestMemPalace_IsAvailable(t *testing.T) {
	mp := NewMemPalace()
	// Just verify it doesn't panic — availability depends on Python env
	_ = mp.IsAvailable()
}

func TestMemPalace_SearchReturnsEmpty_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{bridgePath: "/nonexistent/bridge.py", available: false}
	results, err := mp.Search("test query", "wing", "room", 5)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemPalace_MineNoError_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{bridgePath: "/nonexistent/bridge.py", available: false}
	err := mp.Mine("wing", "room", "some text")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestMemPalace_MineMetaNoError_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{bridgePath: "/nonexistent/bridge.py", available: false}
	err := mp.MineMeta("learning")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestParseBridgeOutput_ValidSearch(t *testing.T) {
	output := `{"status": "ok", "results": [{"text": "store.go created", "wing": "app", "room": "req-1", "similarity": 0.9}]}`
	results, err := parseSearchOutput(output)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Text != "store.go created" {
		t.Errorf("text = %q", results[0].Text)
	}
}

func TestParseBridgeOutput_Error(t *testing.T) {
	output := `{"status": "error", "message": "not installed"}`
	results, err := parseSearchOutput(output)
	if err != nil {
		t.Fatalf("expected no error (graceful), got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on error status")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the implementation**

```go
// internal/memory/mempalace.go
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SearchResult represents a single MemPalace search result.
type SearchResult struct {
	Text       string  `json:"text"`
	Wing       string  `json:"wing"`
	Room       string  `json:"room"`
	Similarity float64 `json:"similarity"`
}

// MemPalace wraps the Python MemPalace bridge for semantic memory.
type MemPalace struct {
	bridgePath  string
	palacePath  string
	available   bool
}

// NewMemPalace creates a MemPalace client, auto-detecting the bridge script.
func NewMemPalace() *MemPalace {
	mp := &MemPalace{}

	// Find bridge script relative to the nxd binary or working directory
	candidates := []string{
		filepath.Join("scripts", "mempalace_bridge.py"),
		filepath.Join("..", "scripts", "mempalace_bridge.py"),
	}
	// Also check relative to the executable
	if execPath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(execPath), "..", "scripts", "mempalace_bridge.py"))
	}

	for _, path := range candidates {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			mp.bridgePath = abs
			break
		}
	}

	// Check if Python and the bridge work
	if mp.bridgePath != "" {
		mp.available = mp.healthCheck()
	}

	return mp
}

// NewMemPalaceWithPath creates a MemPalace with an explicit bridge path and palace path.
func NewMemPalaceWithPath(bridgePath, palacePath string) *MemPalace {
	mp := &MemPalace{
		bridgePath: bridgePath,
		palacePath: palacePath,
	}
	if bridgePath != "" {
		mp.available = mp.healthCheck()
	}
	return mp
}

// IsAvailable returns true if MemPalace is installed and the bridge works.
func (m *MemPalace) IsAvailable() bool {
	return m.available
}

// Search queries MemPalace for semantically similar memories.
// Returns empty results (no error) if MemPalace is unavailable.
func (m *MemPalace) Search(query, wing, room string, maxResults int) ([]SearchResult, error) {
	if !m.available {
		return nil, nil
	}
	args := []string{"search", "--query", query, "--wing", wing, "--results", fmt.Sprintf("%d", maxResults)}
	if room != "" {
		args = append(args, "--room", room)
	}
	output, err := m.runBridge(args...)
	if err != nil {
		return nil, nil // graceful degradation
	}
	return parseSearchOutput(output)
}

// Mine stores text into a specific wing/room.
func (m *MemPalace) Mine(wing, room, text string) error {
	if !m.available {
		return nil
	}
	_, err := m.runBridge("mine", "--wing", wing, "--room", room, "--text", text)
	return err
}

// MineMeta stores cross-project learning into the nxd_meta wing.
func (m *MemPalace) MineMeta(text string) error {
	if !m.available {
		return nil
	}
	_, err := m.runBridge("mine-meta", "--text", text)
	return err
}

// WakeUp returns the L0+L1 context for a wing (~170 tokens).
func (m *MemPalace) WakeUp(wing string) (string, error) {
	if !m.available {
		return "", nil
	}
	output, err := m.runBridge("wake-up", "--wing", wing)
	if err != nil {
		return "", nil
	}
	var resp struct {
		Status  string `json:"status"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return "", nil
	}
	return resp.Context, nil
}

func (m *MemPalace) healthCheck() bool {
	output, err := m.runBridge("health")
	if err != nil {
		return false
	}
	var resp struct{ Status string `json:"status"` }
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return false
	}
	return resp.Status == "ok"
}

func (m *MemPalace) runBridge(args ...string) (string, error) {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}

	cmdArgs := []string{m.bridgePath}
	if m.palacePath != "" {
		cmdArgs = append(cmdArgs, "--palace", m.palacePath)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command(python, cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bridge error: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// parseSearchOutput parses JSON search results from the bridge.
func parseSearchOutput(output string) ([]SearchResult, error) {
	var resp struct {
		Status  string         `json:"status"`
		Results []SearchResult `json:"results"`
		Message string         `json:"message"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, nil // graceful
	}
	if resp.Status != "ok" {
		return nil, nil // graceful
	}
	return resp.Results, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/memory/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/mempalace.go internal/memory/mempalace_test.go
git commit -m "feat: add Go MemPalace client with graceful degradation"
```

---

### Task 3: MemPalace Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add MemoryConfig to config.go**

Read `config.go` first. Add a new config section:

```go
type MemoryConfig struct {
	Enabled    bool   `yaml:"enabled"`
	PalacePath string `yaml:"palace_path,omitempty"`
}
```

Add to Config struct:
```go
Memory    MemoryConfig `yaml:"memory"`
```

- [ ] **Step 2: Add defaults in loader.go**

In `DefaultConfig()`:
```go
Memory: MemoryConfig{
	Enabled: true,
},
```

- [ ] **Step 3: Add test**

```go
func TestDefaultConfig_MemoryDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Memory.Enabled {
		t.Error("expected Memory.Enabled=true by default")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v && go test ./... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/config_test.go
git commit -m "feat: add MemPalace memory config section"
```

---

## Phase 2: Context Sharing & QA Feedback

### Task 4: Agent Context Sharing (PromptContext + Executor)

**Files:**
- Modify: `internal/agent/prompts.go`
- Modify: `internal/engine/executor.go`

- [ ] **Step 1: Add PriorWorkContext and WaveBrief to PromptContext**

In `prompts.go`, add two new fields to `PromptContext`:

```go
PriorWorkContext string // MemPalace search results for prior stories
WaveBrief        string // parallel stories in this wave
```

- [ ] **Step 2: Inject into GoalPrompt**

In the `GoalPrompt()` function, after the existing workflow injections, add:

```go
if ctx.PriorWorkContext != "" {
	goal += "\n\n" + ctx.PriorWorkContext
}
if ctx.WaveBrief != "" {
	goal += "\n\n" + ctx.WaveBrief
}
```

- [ ] **Step 3: Wire MemPalace search into executor**

Read `executor.go`. In the `spawn()` method, after building PromptContext (around line 90-98), add MemPalace query. The executor needs access to a MemPalace client. Add it to the Executor struct:

```go
type Executor struct {
	registry   *runtime.Registry
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
	mempalace  *memory.MemPalace  // NEW
}
```

Update `NewExecutor` to accept and store the MemPalace client. Then in `spawn()`:

```go
// Query MemPalace for prior work context
if e.mempalace != nil && e.mempalace.IsAvailable() {
	repoName := filepath.Base(repoDir)
	query := story.Title + " " + story.Description
	results, _ := e.mempalace.Search(query, repoName, "", 5)
	if len(results) > 0 {
		var sb strings.Builder
		sb.WriteString("## Prior Work in This Requirement\n\n")
		sb.WriteString("The following has already been built. Build on this, do not recreate.\n\n")
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("- %s\n", r.Text))
		}
		promptCtx.PriorWorkContext = sb.String()
	}
}
```

- [ ] **Step 4: Update all NewExecutor call sites**

Grep for `NewExecutor` and add the MemPalace parameter. If MemPalace is not configured, pass `nil`.

- [ ] **Step 5: Run tests**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/prompts.go internal/engine/executor.go
git commit -m "feat: add agent context sharing via MemPalace search in executor"
```

---

### Task 5: Mine Story Diffs + QA Feedback Loop

**Files:**
- Modify: `internal/engine/monitor.go`
- Create: `internal/engine/failure_analyzer.go`
- Create: `internal/engine/failure_analyzer_test.go`

- [ ] **Step 1: Write failure analyzer test**

```go
// internal/engine/failure_analyzer_test.go
package engine

import "testing"

func TestAnalyzeFailure_UndefinedSymbol(t *testing.T) {
	hint := AnalyzeFailure("./store/store.go:42: undefined: NewStore", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
	if hint == "./store/store.go:42: undefined: NewStore" {
		t.Error("expected a helpful hint, not raw output")
	}
}

func TestAnalyzeFailure_MissingPackage(t *testing.T) {
	hint := AnalyzeFailure("cannot find package \"github.com/foo/bar\"", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_TestFailure(t *testing.T) {
	hint := AnalyzeFailure("--- FAIL: TestStore_Get (0.00s)\n    store_test.go:15: expected \"hello\", got \"\"", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_ReviewFeedback(t *testing.T) {
	hint := AnalyzeFailure("", "Missing error handling in Get function")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_UnknownError(t *testing.T) {
	raw := "some weird error nobody anticipated"
	hint := AnalyzeFailure(raw, "")
	if hint != raw {
		t.Error("expected raw output as fallback for unknown errors")
	}
}
```

- [ ] **Step 2: Implement failure analyzer**

```go
// internal/engine/failure_analyzer.go
package engine

import "strings"

// AnalyzeFailure examines QA output and review feedback to produce a targeted
// fix hint. Returns the raw output if no pattern matches.
func AnalyzeFailure(qaOutput, reviewFeedback string) string {
	combined := qaOutput + " " + reviewFeedback
	lower := strings.ToLower(combined)

	patterns := []struct {
		match string
		hint  string
	}{
		{"undefined:", "Build error: undefined symbol. Check that the function/type is exported (capitalized) and properly imported."},
		{"cannot find package", "Missing dependency. Run 'go mod tidy' or add the correct import path."},
		{"imported and not used", "Unused import. Remove the import or use the package."},
		{"declared and not used", "Unused variable. Remove it or use it."},
		{"cannot use", "Type mismatch. Check the function signature and ensure argument types match."},
		{"nil pointer dereference", "Nil pointer. Add a nil check before dereferencing the pointer."},
		{"race condition", "Data race detected. Add sync.Mutex or use channels for shared state."},
		{"connection refused", "Service not running. Check that the required service (database, API) is started."},
		{"permission denied", "Permission error. Check file permissions and user access."},
		{"no such file or directory", "File not found. Check the path exists and is spelled correctly."},
		{"syntax error", "Syntax error. Check for missing brackets, semicolons, or typos."},
		{"timeout", "Operation timed out. Increase timeout or check for deadlocks."},
		{"--- fail:", "Test failure. Read the test output and fix the failing assertion."},
		{"test.*fail", "Test failure. Read the test output and fix the failing assertion."},
		{"missing error handling", "Add error handling: check returned errors and handle them."},
		{"missing test", "Add tests for the new code."},
	}

	for _, p := range patterns {
		if strings.Contains(lower, p.match) {
			return p.hint
		}
	}

	// Fallback: return raw output
	if qaOutput != "" {
		return qaOutput
	}
	return reviewFeedback
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/engine/ -run TestAnalyzeFailure -v`
Expected: PASS

- [ ] **Step 4: Wire into monitor.go — mine diffs + QA feedback**

Read `monitor.go`. Find `postExecutionPipeline` (around line 237). Make these changes:

**After story completion (before review), mine the diff to MemPalace:**

```go
// After detecting story completed, before review:
if m.mempalace != nil && m.mempalace.IsAvailable() {
	diff := m.captureStoryDiff(ag, repoDir)
	if diff != "" {
		repoName := filepath.Base(repoDir)
		summary := fmt.Sprintf("Story %s (%s) completed. Changes:\n%s", ag.StoryID, ag.StoryTitle, truncate(diff, 2000))
		m.mempalace.Mine(repoName, ag.ReqID, summary)
	}
}
```

**On QA failure, feed error back before escalating:**

In the QA failure handling section, before calling `resetStoryToDraft`:

```go
// Capture QA output for retry
var qaOutput string
for _, check := range qaResult.Checks {
	if !check.Passed {
		qaOutput += fmt.Sprintf("[%s] %s\n", check.Name, check.Output)
	}
}

// Try smarter retry first
hint := AnalyzeFailure(qaOutput, "")
retryPrompt := fmt.Sprintf("QA FAILURE — fix this error:\n\n%s\n\nHint: %s\n\nMake the minimal change to fix this.", qaOutput, hint)

// Store as review feedback so the agent sees it on retry
m.storeRetryFeedback(storyID, retryPrompt)
```

The `storeRetryFeedback` method emits a `STORY_REVIEW_FAILED` event with the QA output as feedback, which the executor picks up via `latestReviewFeedback()`.

**After review, mine the feedback:**

```go
if m.mempalace != nil && m.mempalace.IsAvailable() {
	repoName := filepath.Base(repoDir)
	reviewSummary := fmt.Sprintf("Review of %s: %s. %s", ag.StoryID, verdictStr, reviewResult.Summary)
	m.mempalace.Mine(repoName, ag.ReqID, reviewSummary)
}
```

Add `mempalace *memory.MemPalace` field to the Monitor struct and wire it through the constructor.

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/engine/failure_analyzer.go internal/engine/failure_analyzer_test.go internal/engine/monitor.go
git commit -m "feat: add QA feedback loop, failure analysis, and MemPalace diff mining"
```

---

### Task 6: Wave Brief (Parallel Awareness)

**Files:**
- Create: `internal/engine/wave_brief.go`
- Modify: `internal/engine/executor.go` (already modified in Task 4)

- [ ] **Step 1: Write the implementation**

```go
// internal/engine/wave_brief.go
package engine

import (
	"fmt"
	"strings"
)

// WaveStoryInfo describes a story running in the same wave.
type WaveStoryInfo struct {
	ID         string
	Title      string
	OwnedFiles []string
}

// BuildWaveBrief creates a formatted brief of all stories in a wave.
func BuildWaveBrief(currentStoryID string, waveStories []WaveStoryInfo) string {
	if len(waveStories) <= 1 {
		return "" // no brief needed for single-story waves
	}

	var sb strings.Builder
	sb.WriteString("## Parallel Stories in This Wave\n\n")
	sb.WriteString("You are working in parallel with these other agents. Do NOT modify their files.\n\n")

	for _, s := range waveStories {
		if s.ID == currentStoryID {
			continue
		}
		files := "no specific files"
		if len(s.OwnedFiles) > 0 {
			files = strings.Join(s.OwnedFiles, ", ")
		}
		sb.WriteString(fmt.Sprintf("- %s \"%s\" — owns: %s\n", s.ID, s.Title, files))
	}

	return sb.String()
}
```

- [ ] **Step 2: Wire into executor**

In `executor.go`, in the `SpawnAll` method, before spawning each assignment, build the wave brief from all assignments:

```go
// Build wave info from all assignments
var waveStories []WaveStoryInfo
for _, a := range assignments {
	if story, ok := stories[a.StoryID]; ok {
		waveStories = append(waveStories, WaveStoryInfo{
			ID: a.StoryID, Title: story.Title, OwnedFiles: story.OwnedFiles,
		})
	}
}

// In spawn(), add wave brief to PromptContext:
promptCtx.WaveBrief = BuildWaveBrief(a.StoryID, waveStories)
```

- [ ] **Step 3: Run tests**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/engine/wave_brief.go internal/engine/executor.go
git commit -m "feat: add wave brief for parallel story awareness"
```

---

## Phase 3: Observability

### Task 7: Metrics Recorder

**Files:**
- Create: `internal/metrics/recorder.go`
- Create: `internal/metrics/recorder_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/metrics/recorder_test.go
package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecorder_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")

	rec := NewRecorder(path)

	entry := MetricEntry{
		Timestamp:  time.Now(),
		ReqID:      "req-001",
		StoryID:    "s-001",
		Phase:      "plan",
		Model:      "gemma4:26b",
		TokensIn:   1000,
		TokensOut:  500,
		DurationMs: 3200,
		Success:    true,
	}

	if err := rec.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entries, err := rec.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ReqID != "req-001" {
		t.Errorf("ReqID = %q", entries[0].ReqID)
	}
	if entries[0].TokensIn != 1000 {
		t.Errorf("TokensIn = %d", entries[0].TokensIn)
	}
}

func TestRecorder_MultipleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	rec := NewRecorder(path)

	for i := 0; i < 5; i++ {
		rec.Record(MetricEntry{
			ReqID: "req-001", Phase: "execute",
			TokensIn: 100, TokensOut: 50,
			Success: true, Timestamp: time.Now(),
		})
	}

	entries, _ := rec.ReadAll()
	if len(entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(entries))
	}
}

func TestRecorder_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	rec := NewRecorder(path)

	entries, err := rec.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll on empty: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
```

- [ ] **Step 2: Implement recorder**

```go
// internal/metrics/recorder.go
package metrics

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// MetricEntry records a single LLM interaction.
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

// Recorder appends metrics to a JSONL file.
type Recorder struct {
	path string
	mu   sync.Mutex
}

// NewRecorder creates a recorder writing to the given path.
func NewRecorder(path string) *Recorder {
	return &Recorder{path: path}
}

// Record appends a metric entry to the file.
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

// ReadAll reads all metric entries from the file.
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
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/metrics/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/metrics/recorder.go internal/metrics/recorder_test.go
git commit -m "feat: add metrics recorder with JSONL persistence"
```

---

### Task 8: MetricsClient Wrapper + Reporter

**Files:**
- Create: `internal/metrics/client.go`
- Create: `internal/metrics/reporter.go`

- [ ] **Step 1: Implement MetricsClient**

```go
// internal/metrics/client.go
package metrics

import (
	"context"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// MetricsClient wraps an llm.Client and records metrics for every call.
type MetricsClient struct {
	inner    llm.Client
	recorder *Recorder
	reqID    string
	phase    string
	role     string
}

// NewMetricsClient wraps a client with metrics recording.
func NewMetricsClient(inner llm.Client, recorder *Recorder, reqID, phase, role string) *MetricsClient {
	return &MetricsClient{
		inner: inner, recorder: recorder,
		reqID: reqID, phase: phase, role: role,
	}
}

// WithPhase returns a copy with a different phase label.
func (m *MetricsClient) WithPhase(phase string) *MetricsClient {
	return &MetricsClient{
		inner: m.inner, recorder: m.recorder,
		reqID: m.reqID, phase: phase, role: m.role,
	}
}

// WithRole returns a copy with a different role label.
func (m *MetricsClient) WithRole(role string) *MetricsClient {
	return &MetricsClient{
		inner: m.inner, recorder: m.recorder,
		reqID: m.reqID, phase: m.phase, role: role,
	}
}

// Complete calls the inner client and records metrics.
func (m *MetricsClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	start := time.Now()
	resp, err := m.inner.Complete(ctx, req)

	entry := MetricEntry{
		Timestamp:  start,
		ReqID:      m.reqID,
		Phase:      m.phase,
		Role:       m.role,
		Model:      req.Model,
		TokensIn:   resp.Usage.InputTokens,
		TokensOut:  resp.Usage.OutputTokens,
		DurationMs: time.Since(start).Milliseconds(),
		Success:    err == nil,
	}
	m.recorder.Record(entry) // fire-and-forget, don't fail the call

	return resp, err
}
```

- [ ] **Step 2: Implement reporter**

```go
// internal/metrics/reporter.go
package metrics

import (
	"fmt"
	"io"
	"time"
)

// Summary aggregates metrics for display.
type Summary struct {
	TotalRequirements int
	TotalStories      int
	TotalTokensIn     int
	TotalTokensOut    int
	TotalDurationMs   int64
	SuccessCount      int
	FailureCount      int
	EscalationCount   int
	ByPhase           map[string]PhaseSummary
}

// PhaseSummary aggregates metrics for a single phase.
type PhaseSummary struct {
	Count     int
	TokensIn  int
	TokensOut int
}

// Summarize aggregates a list of metric entries.
func Summarize(entries []MetricEntry) Summary {
	s := Summary{ByPhase: make(map[string]PhaseSummary)}

	reqs := map[string]bool{}
	stories := map[string]bool{}

	for _, e := range entries {
		reqs[e.ReqID] = true
		if e.StoryID != "" {
			stories[e.StoryID] = true
		}
		s.TotalTokensIn += e.TokensIn
		s.TotalTokensOut += e.TokensOut
		s.TotalDurationMs += e.DurationMs
		if e.Success {
			s.SuccessCount++
		} else {
			s.FailureCount++
		}
		if e.Escalated {
			s.EscalationCount++
		}

		ps := s.ByPhase[e.Phase]
		ps.Count++
		ps.TokensIn += e.TokensIn
		ps.TokensOut += e.TokensOut
		s.ByPhase[e.Phase] = ps
	}

	s.TotalRequirements = len(reqs)
	s.TotalStories = len(stories)
	return s
}

// PrintSummary writes a human-readable metrics summary.
func PrintSummary(w io.Writer, s Summary) {
	total := s.SuccessCount + s.FailureCount
	successRate := 0.0
	if total > 0 {
		successRate = float64(s.SuccessCount) / float64(total) * 100
	}

	fmt.Fprintf(w, "Requirements: %d | Stories: %d\n", s.TotalRequirements, s.TotalStories)
	fmt.Fprintf(w, "LLM calls: %d (%.0f%% success)\n", total, successRate)
	fmt.Fprintf(w, "Escalations: %d\n\n", s.EscalationCount)

	totalTokens := s.TotalTokensIn + s.TotalTokensOut
	fmt.Fprintf(w, "Token usage:\n")
	for phase, ps := range s.ByPhase {
		phaseTotal := ps.TokensIn + ps.TokensOut
		fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", phase+":", phaseTotal/1000, ps.Count)
	}
	fmt.Fprintf(w, "  %-14s %6dK tokens\n", "Total:", totalTokens/1000)

	if total > 0 {
		avgMs := s.TotalDurationMs / int64(total)
		fmt.Fprintf(w, "\nAvg latency: %s per call\n", time.Duration(avgMs)*time.Millisecond)
	}
}
```

- [ ] **Step 3: Run tests and build**

Run: `go test ./internal/metrics/ -v && go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/metrics/client.go internal/metrics/reporter.go
git commit -m "feat: add MetricsClient wrapper and reporter with aggregation"
```

---

### Task 9: CLI Commands (metrics + watch)

**Files:**
- Create: `internal/cli/metrics.go`
- Create: `internal/cli/watch.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Implement nxd metrics command**

```go
// internal/cli/metrics.go
package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show pipeline metrics and token usage",
		RunE:  runMetrics,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func runMetrics(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	jsonMode, _ := cmd.Flags().GetBool("json")

	stateDir := expandHome(cfg.Workspace.StateDir)
	metricsPath := filepath.Join(stateDir, "metrics.jsonl")
	rec := metrics.NewRecorder(metricsPath)

	entries, err := rec.ReadAll()
	if err != nil {
		return fmt.Errorf("read metrics: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No metrics recorded yet. Run 'nxd req' to start collecting.")
		return nil
	}

	summary := metrics.Summarize(entries)

	if jsonMode {
		data, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	} else {
		metrics.PrintSummary(cmd.OutOrStdout(), summary)
	}
	return nil
}
```

- [ ] **Step 2: Implement nxd watch command**

```go
// internal/cli/watch.go
package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream pipeline events in real time",
		Long:  "Polls the event store and prints new events as they arrive. Ctrl+C to stop.",
		RunE:  runWatch,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runWatch(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Watching for events... (Ctrl+C to stop)")
	fmt.Fprintln(out)

	lastSeen := 0
	ctx := cmd.Context()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		events, err := s.Events.List(state.EventFilter{})
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if len(events) > lastSeen {
			for _, evt := range events[lastSeen:] {
				ts := evt.Timestamp.Format("15:04:05")
				line := fmt.Sprintf("[%s] %s", ts, evt.Type)
				if evt.StoryID != "" {
					line += " " + evt.StoryID
				}
				if evt.AgentID != "" {
					line += " agent=" + evt.AgentID
				}
				// Add payload summary for key events
				payload := state.DecodePayload(evt.Payload)
				if title, ok := payload["title"].(string); ok {
					line += fmt.Sprintf(" %q", title)
				}
				if status, ok := payload["status"].(string); ok {
					line += " status=" + status
				}
				fmt.Fprintln(out, line)
			}
			lastSeen = len(events)
		}

		time.Sleep(500 * time.Millisecond)
	}
}
```

- [ ] **Step 3: Register commands in root.go**

Add to `init()`:
```go
rootCmd.AddCommand(newMetricsCmd())
rootCmd.AddCommand(newWatchCmd())
```

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/nxd/ && ./nxd --help | grep -E "metrics|watch"`
Expected: Both commands appear in help

- [ ] **Step 5: Commit**

```bash
git add internal/cli/metrics.go internal/cli/watch.go internal/cli/root.go
git commit -m "feat: add nxd metrics and nxd watch CLI commands"
```

---

## Phase 4: Agent Intelligence

### Task 10: Convention Detection (Investigator Enhancement)

**Files:**
- Modify: `internal/agent/investigator.go`
- Modify: `internal/engine/investigator.go`
- Modify: `internal/engine/investigator_test.go`

- [ ] **Step 1: Add Convention type to investigator.go (engine)**

```go
type Convention struct {
	Area        string `json:"area"`
	Pattern     string `json:"pattern"`
	ExampleFile string `json:"example_file"`
}
```

Add `Conventions []Convention` to `InvestigationReport`.

- [ ] **Step 2: Update InvestigatorSystemPrompt**

In `internal/agent/investigator.go`, append Phase 7 to the system prompt:

```
Phase 7: CONVENTION DETECTION
- Read 3 existing handler/controller files — extract the pattern (naming, error handling, response format)
- Read 3 existing test files — extract testing style (testify? table-driven? test helpers?)
- Read config/setup files — detect frameworks (Gin, Echo, Chi, Express, Django)
- Produce: conventions[] with {area, pattern, example_file}
```

- [ ] **Step 3: Update submit_report schema**

Add `conventions` to the submit_report tool's JSON Schema:

```json
"conventions": {"type": "array", "items": {"type": "object", "properties": {"area": {"type": "string"}, "pattern": {"type": "string"}, "example_file": {"type": "string"}}, "required": ["area", "pattern"]}}
```

- [ ] **Step 4: Update parseInvestigationReport**

In `internal/engine/investigator.go`, add `Conventions []Convention` to the raw parse struct and map it to the report.

- [ ] **Step 5: Update test**

Add to investigator_test.go: a test where submit_report includes conventions, verify they appear in the parsed report.

- [ ] **Step 6: Inject conventions into agent prompts**

In `internal/agent/prompts.go`, when `InvestigationReport` contains conventions text, format them:

```
## Codebase Conventions (follow these)

- Handlers: use Chi router, return JSON. See: internal/handler/user.go
- Tests: table-driven with testify. See: internal/handler/user_test.go
```

This is already handled by the InvestigationReport field (which is formatted markdown). The planner formats the report including conventions before injecting.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/engine/ -run TestInvestigator -v && go test ./... -count=1`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/agent/investigator.go internal/engine/investigator.go internal/engine/investigator_test.go
git commit -m "feat: add convention detection to Investigator (Phase 7)"
```

---

## Phase 5: Wiring Tests + Verification

### Task 11: Wiring Tests for All New Features

**Files:**
- Modify: `internal/engine/wiring_test.go`
- Modify: `internal/llm/wiring_test.go`

- [ ] **Step 1: Add 7 new wiring tests**

Append to `internal/engine/wiring_test.go`:

```go
func TestWiring_MemPalaceGracefulDegradation(t *testing.T) {
	// MemPalace unavailable → empty results, no errors
	mp := &memory.MemPalace{} // zero value = unavailable
	results, err := mp.Search("test", "wing", "room", 5)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected 0 results when unavailable")
	}
}

func TestWiring_QAFeedbackReachesAgent(t *testing.T) {
	// QA error output should be formatted as ReviewFeedback
	qaOutput := "go build: undefined: NewStore"
	hint := AnalyzeFailure(qaOutput, "")
	feedback := fmt.Sprintf("QA FAILURE — fix this error:\n\n%s\n\nHint: %s", qaOutput, hint)

	ctx := agent.PromptContext{
		StoryTitle: "Build store", StoryDescription: "create store",
		ReviewFeedback: feedback,
	}
	goal := agent.GoalPrompt(agent.RoleSenior, ctx)
	if !strings.Contains(goal, "QA FAILURE") {
		t.Error("expected QA feedback in goal prompt")
	}
	if !strings.Contains(goal, "undefined: NewStore") {
		t.Error("expected specific error in goal prompt")
	}
}

func TestWiring_WaveBriefInjected(t *testing.T) {
	stories := []WaveStoryInfo{
		{ID: "s-001", Title: "Store package", OwnedFiles: []string{"store/store.go"}},
		{ID: "s-002", Title: "HTTP API", OwnedFiles: []string{"main.go"}},
	}
	brief := BuildWaveBrief("s-001", stories)
	if !strings.Contains(brief, "s-002") {
		t.Error("expected s-002 in wave brief")
	}
	if !strings.Contains(brief, "main.go") {
		t.Error("expected owned files in wave brief")
	}
	if strings.Contains(brief, "s-001") {
		t.Error("current story should NOT appear in its own wave brief")
	}
}

func TestWiring_FailureAnalyzerPatterns(t *testing.T) {
	tests := []struct{ input, mustContain string }{
		{"undefined: NewStore", "undefined symbol"},
		{"cannot find package", "Missing dependency"},
		{"--- FAIL: TestFoo", "Test failure"},
	}
	for _, tt := range tests {
		hint := AnalyzeFailure(tt.input, "")
		if !strings.Contains(strings.ToLower(hint), strings.ToLower(tt.mustContain)) {
			t.Errorf("AnalyzeFailure(%q) = %q, expected to contain %q", tt.input, hint, tt.mustContain)
		}
	}
}

func TestWiring_MetricsRecorded(t *testing.T) {
	dir := t.TempDir()
	rec := metrics.NewRecorder(filepath.Join(dir, "metrics.jsonl"))

	inner := llm.NewReplayClient(llm.CompletionResponse{
		Content: "test", Model: "gemma4:26b",
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 50},
	})

	mc := metrics.NewMetricsClient(inner, rec, "req-001", "plan", "tech_lead")
	mc.Complete(context.Background(), llm.CompletionRequest{Model: "gemma4:26b"})

	entries, _ := rec.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("expected 1 metric entry, got %d", len(entries))
	}
	if entries[0].TokensIn != 100 {
		t.Errorf("TokensIn = %d, want 100", entries[0].TokensIn)
	}
}

func TestWiring_ConventionsDetected(t *testing.T) {
	// Verify Convention type exists on InvestigationReport
	report := InvestigationReport{
		Summary: "test",
		Conventions: []Convention{
			{Area: "test", Pattern: "table-driven", ExampleFile: "foo_test.go"},
		},
	}
	if len(report.Conventions) != 1 {
		t.Error("expected conventions field on report")
	}
}
```

Add necessary imports: `memory`, `metrics`, `agent`, `llm`, `context`, `strings`, `fmt`, `filepath`.

**NOTE:** Some of these tests reference types from other packages. Check if the wiring test file is in `package engine` (internal) — if so, it can access unexported engine types but needs to import `memory`, `metrics` packages explicitly. Read the existing wiring_test.go to determine the package.

- [ ] **Step 2: Run all wiring tests**

Run: `go test ./internal/engine/ -run TestWiring -v`
Expected: All 33 wiring tests pass (26 existing + 7 new)

- [ ] **Step 3: Commit**

```bash
git add internal/engine/wiring_test.go
git commit -m "test: add wiring tests for MemPalace, QA feedback, wave brief, metrics, conventions"
```

---

### Task 12: Final Verification

- [ ] **Step 1: Full test suite**

Run: `go test ./... -race -count=1`
Expected: All packages pass with no races

- [ ] **Step 2: Build binary**

Run: `go build -o /tmp/nxd ./cmd/nxd/`
Expected: Success

- [ ] **Step 3: CLI smoke test**

Run:
```bash
/tmp/nxd --help | grep -E "metrics|watch"
cd /tmp && rm -f nxd.yaml && /tmp/nxd init
/tmp/nxd metrics
/tmp/nxd config show | grep -A2 memory
```
Expected: metrics and watch appear in help, metrics shows "No metrics yet", config shows memory section

- [ ] **Step 4: Commit any fixes**

```bash
git status
```
