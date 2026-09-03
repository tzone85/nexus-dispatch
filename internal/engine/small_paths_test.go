package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestTimelineLabel_AllEventTypes(t *testing.T) {
	stories := map[string]state.Story{"s1": {ID: "s1", Title: "Tokens"}}
	now := time.Now()
	cases := []struct {
		typ     state.EventType
		story   string
		payload map[string]any
		want    string
	}{
		{state.EventReqSubmitted, "", nil, "requirement submitted"},
		{state.EventReqPlanned, "", map[string]any{"story_count": 4.0}, "planned: 4 stories"},
		{state.EventReqPlanned, "", nil, "requirement planned"},
		{state.EventStoryCreated, "s1", nil, `story created: s1 ("Tokens")`},
		{state.EventStoryAssigned, "s1", nil, `assigned s1 ("Tokens") → tester`},
		{state.EventStoryStarted, "s1", nil, `started s1 ("Tokens")`},
		{state.EventStoryCompleted, "s1", nil, `agent finished s1 ("Tokens")`},
		{state.EventStoryReviewPassed, "s1", nil, `review passed: s1 ("Tokens")`},
		{state.EventStoryQAPassed, "s1", nil, `QA passed: s1 ("Tokens")`},
		{state.EventStoryQAFailed, "s1", nil, `QA FAILED: s1 ("Tokens")`},
		{state.EventStorySecurityPassed, "s1", nil, `security gate passed: s1 ("Tokens")`},
		{state.EventStorySecurityFailed, "s1", nil, `security gate FAILED: s1 ("Tokens")`},
		{state.EventStoryEscalated, "s1", nil, `escalated s1 ("Tokens")`},
		{state.EventReqPaused, "", map[string]any{"id": "r"}, "requirement paused"},
		{state.EventReqResumed, "", nil, "requirement resumed"},
		{state.EventReqBudgetWarning, "", map[string]any{"spent_usd": 3.5, "budget_usd": 4.0}, "budget warning: $3.50 of $4.00 spent"},
		{state.EventHumanReviewNeeded, "s1", nil, "human review needed"},
		{state.EventReqBlocked, "", nil, "requirement BLOCKED (mainline stayed red)"},
		{state.EventReqCompleted, "", nil, "requirement completed"},
		{state.EventType("CUSTOM_THING"), "s9", nil, "CUSTOM_THING: s9"},
		{state.EventStoryMerged, "s-untitled", nil, "merged s-untitled"},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ), func(t *testing.T) {
			got := timelineLabel(tlEvent(now, tc.typ, tc.story, tc.payload), stories)
			if got != tc.want {
				t.Errorf("timelineLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFlexibleStringSlice_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"array", `["a.go", "b.go"]`, []string{"a.go", "b.go"}, false},
		{"null", `null`, nil, false},
		{"comma string", `"src/a.go, src/b.go,,"`, []string{"src/a.go", "src/b.go"}, false},
		{"blank string", `"   "`, nil, false},
		{"number is rejected", `42`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got FlexibleStringSlice
			err := json.Unmarshal([]byte(tc.in), &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if !strings.Contains(err.Error(), "FlexibleStringSlice: cannot decode 42") {
					t.Errorf("error = %q", err)
				}
				return
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	// Inside a struct the field is optional and absent → empty.
	var s struct {
		Files FlexibleStringSlice `json:"owned_files"`
	}
	if err := json.Unmarshal([]byte(`{"owned_files": ""}`), &s); err != nil || len(s.Files) != 0 {
		t.Errorf("empty string field = %v (err=%v)", s.Files, err)
	}
}

func TestExtractJSON_PreambleAndStrayBrackets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"bracket in preamble", `Score [10/10]: here it is ["a", "b"]`, `["a", "b"]`},
		{"stray closing after payload", `{"a": [1, 2]} }`, `{"a": [1, 2]}`},
		{"array with braces inside strings", `note: ["{not json}", "x"] trailing`, `["{not json}", "x"]`},
		{"unbalanced returns input", `{"open": [1, 2`, `{"open": [1, 2`},
		{"comments inside object", "{\n\t\"a\": 1, // one\n\t\"b\": 2,\n}", "{\n\t\"a\": 1, \n\t\"b\": 2\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.in)
			if got != tc.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.name != "unbalanced returns input" && !json.Valid([]byte(got)) {
				t.Errorf("result is not valid JSON: %q", got)
			}
		})
	}
}

func TestEmitHumanReviewNeeded_CriteriaAndQAPatterns(t *testing.T) {
	t.Run("criteria thrashing", func(t *testing.T) {
		m, es, _ := newHumanReviewMonitor(t)
		for i := 0; i < 2; i++ {
			es.Append(state.NewEvent(state.EventStoryProgress, "agent", "s-c", map[string]any{
				"is_error": true, "detail": "criteria failed: go build ./...",
			}))
		}
		es.Append(state.NewEvent(state.EventStoryProgress, "agent", "s-c", map[string]any{"iteration": 1}))
		m.emitHumanReviewNeeded(state.Story{ID: "s-c", ReqID: "r-1"}, "story exhausted all escalation tiers")

		got := mostRecentHumanReview(t, es)
		if got["failure_pattern"] != "criteria_thrashing" {
			t.Fatalf("pattern = %v, want criteria_thrashing", got["failure_pattern"])
		}
		counters := got["counters"].(map[string]any)
		if counters["criteria_failed"].(float64) != 2 {
			t.Errorf("criteria_failed counter = %v, want 2", counters["criteria_failed"])
		}
		suggestions, _ := got["suggested_directives"].([]any)
		if len(suggestions) != 2 || !strings.Contains(suggestions[1].(string), "split this story") {
			t.Errorf("suggestions = %v", suggestions)
		}
	})

	t.Run("qa failing dominates", func(t *testing.T) {
		m, es, _ := newHumanReviewMonitor(t)
		for i := 0; i < 3; i++ {
			es.Append(state.NewEvent(state.EventStoryQAFailed, "qa", "s-q", nil))
		}
		es.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-q", nil))
		m.emitHumanReviewNeeded(state.Story{ID: "s-q", ReqID: "r-1"}, "story exhausted all escalation tiers")

		got := mostRecentHumanReview(t, es)
		if got["failure_pattern"] != "qa_failing" {
			t.Fatalf("pattern = %v, want qa_failing", got["failure_pattern"])
		}
		counters := got["counters"].(map[string]any)
		if counters["qa_failed"].(float64) != 3 || counters["review_failed"].(float64) != 1 {
			t.Errorf("counters = %v", counters)
		}
	})

	t.Run("merge error events counted", func(t *testing.T) {
		m, es, _ := newHumanReviewMonitor(t)
		es.Append(state.NewEvent("STORY_MERGE_ERROR", "merger", "s-m", nil))
		m.emitHumanReviewNeeded(state.Story{ID: "s-m", ReqID: "r-1"}, "story exhausted all escalation tiers")
		got := mostRecentHumanReview(t, es)
		if got["failure_pattern"] != "merge_error" {
			t.Fatalf("pattern = %v, want merge_error", got["failure_pattern"])
		}
		if c := got["counters"].(map[string]any)["merge_error"].(float64); c != 1 {
			t.Errorf("merge_error counter = %v, want 1", c)
		}
	})
}

// failingAppendStore rejects appends of one event type.
type failingAppendStore struct {
	state.EventStore
	reject state.EventType
}

func (s failingAppendStore) Append(e state.Event) error {
	if e.Type == s.reject {
		return errors.New("disk full")
	}
	return s.EventStore.Append(e)
}

func TestQARun_SuccessCriteriaAndStoreErrors(t *testing.T) {
	t.Run("declarative criteria are evaluated alongside commands", func(t *testing.T) {
		es, ps := pipelineStores(t)
		seedCapacityStory(t, es, ps, "REQ-QA", "s-qa")
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		qa := NewQA(QAConfig{SuccessCriteria: []criteria.Criterion{
			{Type: criteria.TypeFileExists, Target: "present.txt"},
			{Type: criteria.TypeFileExists, Target: "missing.txt"},
		}}, &scriptedRunner{}, es, ps)

		result, err := qa.ForAttempt("s-qa-a1").Run(context.Background(), "s-qa", dir)
		if err != nil {
			t.Fatal(err)
		}
		if result.Passed || len(result.Checks) != 2 {
			t.Fatalf("result = %+v, want 2 checks with one failure", result)
		}
		if result.Checks[0].Name != "criteria:file_exists(present.txt)" || !result.Checks[0].Passed {
			t.Errorf("check[0] = %+v", result.Checks[0])
		}
		if result.Checks[1].Passed || result.Checks[1].Output == "" {
			t.Errorf("check[1] = %+v, want a failure with detail", result.Checks[1])
		}
		failed, _ := es.List(state.EventFilter{Type: state.EventStoryQAFailed, StoryID: "s-qa"})
		if len(failed) != 1 || failed[0].AttemptID != "s-qa-a1" {
			t.Fatalf("STORY_QA_FAILED = %+v", failed)
		}
		p := state.DecodePayload(failed[0].Payload)
		if fc, _ := p["failed_checks"].([]any); len(fc) != 1 || fc[0] != "criteria:file_exists(missing.txt)" {
			t.Errorf("failed_checks = %v", p["failed_checks"])
		}
	})

	t.Run("start event append failure aborts", func(t *testing.T) {
		es, ps := pipelineStores(t)
		qa := NewQA(QAConfig{TestCommand: "go test"}, &scriptedRunner{}, failingAppendStore{es, state.EventStoryQAStarted}, ps)
		_, err := qa.Run(context.Background(), "s-x", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "emit qa started: disk full") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("result event append failure is returned with the result", func(t *testing.T) {
		es, ps := pipelineStores(t)
		runner := &scriptedRunner{output: "ok"}
		qa := NewQA(QAConfig{TestCommand: "go test"}, runner, failingAppendStore{es, state.EventStoryQAPassed}, ps)
		result, err := qa.Run(context.Background(), "s-x", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "emit qa result: disk full") {
			t.Fatalf("err = %v", err)
		}
		if !result.Passed || runner.calls != 1 {
			t.Errorf("result = %+v calls = %d; the checks must still have run", result, runner.calls)
		}
	})
}

// holdFlock starts a helper process that holds an exclusive flock on path
// and returns once the lock is held. The process is killed on cleanup.
func holdFlock(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	cmd := exec.Command("flock", "-x", path, "-c", "echo ready; exec sleep 60")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("flock helper did not report ready: %q (%v)", line, err)
	}
}

func TestAcquireLock_HeldFlockWithDeadOwnerIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "nxd.lock")
	// The recorded owner is dead, but some other process still holds the
	// flock on the file (e.g. an orphaned child of the dead pipeline).
	stale, _ := json.Marshal(lockInfo{PID: 999999999, Command: "ghost", StartedAt: time.Now().UTC()})
	if err := os.WriteFile(lockPath, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	holdFlock(t, lockPath)

	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock should reclaim a lock whose recorded owner is dead: %v", err)
	}
	defer lock.Release()

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("lock now owned by pid %d, want %d", info.PID, os.Getpid())
	}
}

func TestAcquireLock_HeldFlockWithUnreadableInfo(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "nxd.lock")
	if err := os.WriteFile(lockPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	holdFlock(t, lockPath)

	_, err := AcquireLock(dir)
	if err == nil || !strings.Contains(err.Error(), "lock is held and lock info unreadable") {
		t.Fatalf("err = %v", err)
	}
}

func TestAcquireLock_UnwritableDir(t *testing.T) {
	_, err := AcquireLock(filepath.Join(t.TempDir(), "missing", "deeper"))
	if err == nil || !strings.Contains(err.Error(), "opening lock file") {
		t.Fatalf("err = %v", err)
	}
}

func TestPipelineLock_ReleaseTwiceIsNoop(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release must be a no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nxd.lock")); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed after release (err=%v)", err)
	}
}
