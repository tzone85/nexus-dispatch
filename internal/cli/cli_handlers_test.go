package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/improver"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// mkRunCmd builds a minimal *cobra.Command pre-wired with the
// --config persistent flag the runFoo handlers depend on, plus a
// stdout/stderr buffer the tests assert against. Returns the command
// + a buffer pointer so tests can inspect output without going
// through Cobra's argument parser. This is the pattern the roadmap
// (PR 1) named for direct-call tests of cli command handlers.
func mkRunCmd(t *testing.T, cfgPath string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.Flags().String("config", cfgPath, "")
	cmd.SetContext(context.Background())
	return cmd, &buf
}

// TestRunImprove_NoMetricsHealthyMessage covers the empty-state path:
// when no metrics.jsonl exists, the analyzers find nothing and the
// command prints the "looking healthy" line. Closes the gap that left
// runImprove at 0% — every code path in the function flows through
// this branch first.
func TestRunImprove_NoMetricsHealthyMessage(t *testing.T) {
	env := setupTestEnv(t)
	cmd, buf := mkRunCmd(t, env.Config)
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")

	if err := runImprove(cmd, nil); err != nil {
		t.Fatalf("runImprove: %v", err)
	}
	if !strings.Contains(buf.String(), "looking healthy") {
		t.Errorf("expected healthy message; got %q", buf.String())
	}
}

// TestRunImprove_PrintsSuggestions seeds bad metrics.jsonl so the
// MetricsAnalyzer fires the high-failure-rate branch. Confirms the
// formatted output contains severity, title, and Action lines.
func TestRunImprove_PrintsSuggestions(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := expandHome(filepath.Dir(env.Config) + "/.nxd")
	rec := metrics.NewRecorder(filepath.Join(stateDir, "metrics.jsonl"))
	defer rec.Close()
	// 5 failures out of 6 calls = ~83% failure rate, well over the 25%
	// threshold → triggers metrics.high_failure_rate (critical).
	for i := range 6 {
		_ = rec.Record(metrics.MetricEntry{
			ReqID: "r", StoryID: "s1", Phase: "execute",
			TokensIn: 100, TokensOut: 50, DurationMs: 500,
			Success: i == 0, // only the first succeeds
		})
	}

	cmd, buf := mkRunCmd(t, env.Config)
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")

	if err := runImprove(cmd, nil); err != nil {
		t.Fatalf("runImprove: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[critical]") {
		t.Errorf("expected [critical] in output; got %q", out)
	}
	if !strings.Contains(out, "failure rate") {
		t.Errorf("expected failure rate suggestion; got %q", out)
	}
	// Persisted JSON must exist on disk for the dashboard to read.
	if _, err := os.Stat(filepath.Join(stateDir, "improvements.json")); err != nil {
		t.Errorf("improvements.json not persisted: %v", err)
	}
}

// TestRunImprove_JSONOutput exercises the --json branch. The output
// must round-trip through json.Unmarshal so consumers (CI, scripts)
// can rely on the schema.
func TestRunImprove_JSONOutput(t *testing.T) {
	env := setupTestEnv(t)
	cmd, buf := mkRunCmd(t, env.Config)
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")
	_ = cmd.Flags().Set("json", "true")

	if err := runImprove(cmd, nil); err != nil {
		t.Fatalf("runImprove: %v", err)
	}
	var got []improver.Suggestion
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Errorf("json output not parseable: %v\nout=%q", err, buf.String())
	}
}

// TestRunImprove_BadConfigFailsLoudly proves the precondition: an
// invalid config path returns an error rather than silently falling
// through to "no suggestions" (which would make config typos
// invisible).
func TestRunImprove_BadConfigFailsLoudly(t *testing.T) {
	cmd, _ := mkRunCmd(t, "/no/such/nxd.yaml")
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")

	if err := runImprove(cmd, nil); err == nil {
		t.Fatal("expected error for missing config")
	}
}

// TestRunMergeStory_RejectsNonMergeReady covers the precondition that
// protects the merge command from being called on a story that
// hasn't passed review + QA yet.
func TestRunMergeStory_RejectsNonMergeReady(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-1", "Test", "/tmp")
	seedTestStory(t, env, "STORY-NOT-READY", "REQ-1", "Test story", 3)
	// Story status is "draft" (default after STORY_CREATED).

	cmd, _ := mkRunCmd(t, env.Config)
	err := runMergeStory(cmd, []string{"STORY-NOT-READY"})
	if err == nil {
		t.Fatal("expected error for non-merge_ready story")
	}
	if !strings.Contains(err.Error(), "merge_ready") {
		t.Errorf("error should mention merge_ready, got: %v", err)
	}
}

// TestRunMergeStory_NotFound rejects unknown story IDs with a clear
// error rather than panicking on the nil result from GetStory.
func TestRunMergeStory_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd, _ := mkRunCmd(t, env.Config)

	err := runMergeStory(cmd, []string{"DOES-NOT-EXIST"})
	if err == nil {
		t.Fatal("expected error for unknown story")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

// TestRunReviewStory_PrintsStoryFields covers the no-branch path: a
// story without a git branch field still renders ID/title/status in
// the review header. Closes the basic-rendering gap.
func TestRunReviewStory_PrintsStoryFields(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-1", "Test", "/tmp")
	seedTestStory(t, env, "STORY-VIEW", "REQ-1", "Pretty Title", 3)

	cmd, buf := mkRunCmd(t, env.Config)
	if err := runReviewStory(cmd, []string{"STORY-VIEW"}); err != nil {
		t.Fatalf("runReviewStory: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"STORY-VIEW", "Pretty Title", "Status:", "Complexity:"} {
		if !strings.Contains(out, want) {
			t.Errorf("review output missing %q:\n%s", want, out)
		}
	}
}

// TestRunReviewStory_NotFound mirrors the merge variant — clear
// error, not a panic, when the story isn't in the projection.
func TestRunReviewStory_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd, _ := mkRunCmd(t, env.Config)

	err := runReviewStory(cmd, []string{"GHOST-STORY"})
	if err == nil {
		t.Fatal("expected error for unknown story")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

// TestPrintEstimateJSON_ProducesValidJSON covers the JSON formatter
// branch of the estimate command. printEstimateJSON uses fmt.Println
// (writes to os.Stdout, not cmd.OutOrStdout), so we redirect stdout
// via os.Pipe.
func TestPrintEstimateJSON_ProducesValidJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = prevStdout
	})

	est := engine.Estimate{
		Requirement: "Test estimate",
		IsQuick:     false,
		Summary: engine.EstimateSummary{
			Rate: 150, StoryCount: 1, TotalPoints: 3,
			HoursLow: 2, HoursHigh: 4,
			QuoteLow: 300, QuoteHigh: 600,
			LLMCost: 0, MarginPercent: 50,
		},
	}
	if err := printEstimateJSON(est); err != nil {
		t.Fatalf("printEstimateJSON: %v", err)
	}
	w.Close()

	var captured bytes.Buffer
	_, _ = captured.ReadFrom(r)

	var parsed map[string]any
	if err := json.Unmarshal(captured.Bytes(), &parsed); err != nil {
		t.Errorf("emitted JSON not parseable: %v\nout=%s", err, captured.String())
	}
	if parsed["requirement"] != "Test estimate" {
		t.Errorf("round-trip lost requirement: %v", parsed)
	}
}

// TestRunEstimate_QuickHeuristic covers the --quick branch which
// skips the LLM call entirely and uses heuristic decomposition.
// Easiest end-to-end path through runEstimate without needing
// Ollama or fixtures.
func TestRunEstimate_QuickHeuristic(t *testing.T) {
	env := setupTestEnv(t)
	cmd, _ := mkRunCmd(t, env.Config)
	cmd.Flags().Bool("quick", true, "")
	cmd.Flags().Float64("rate", 0, "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("save", false, "")
	_ = cmd.Flags().Set("quick", "true")

	// runEstimate writes via fmt.Println (not cmd.OutOrStdout) — we
	// just confirm it doesn't return an error; output capture isn't
	// the contract we're locking.
	if err := runEstimate(cmd, []string{"Add a CLI flag"}); err != nil {
		t.Fatalf("runEstimate quick: %v", err)
	}
}

// TestRunImprove_OnlineFeedFlag covers the --feed URL branch by
// pointing it at a tiny localhost test server. Confirms the
// improver wires the HTTPFeed when --feed is non-empty.
func TestRunImprove_OnlineFeedFlag(t *testing.T) {
	env := setupTestEnv(t)

	// Feed URL that returns a single curated tip.
	feedSrv := startFeedServer(t, []byte(`[{"id":"online.demo","title":"Demo","severity":"info","source":"online"}]`))
	defer feedSrv.Close()

	cmd, buf := mkRunCmd(t, env.Config)
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")
	_ = cmd.Flags().Set("feed", feedSrv.URL)

	if err := runImprove(cmd, nil); err != nil {
		t.Fatalf("runImprove --feed: %v", err)
	}
	if !strings.Contains(buf.String(), "Demo") {
		t.Errorf("expected feed tip in output; got %q", buf.String())
	}
}

// TestRunDirect_AppendsUserDirective covers the happy path: a known
// requirement ID + an instruction emits a USER_DIRECTIVE event.
// runDirect was at 69% — only the validation branches were tested.
// This locks down the actual side effect (event append) operators
// rely on for mid-run redirects.
func TestRunDirect_AppendsUserDirective(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-D", "Test", "/tmp")

	cmd, _ := mkRunCmd(t, env.Config)
	cmd.Flags().String("message-file", "", "")

	if err := runDirect(cmd, []string{"REQ-D", "use", "feature", "flag", "X"}); err != nil {
		t.Fatalf("runDirect: %v", err)
	}

	// Open a fresh handle to events.jsonl since the test env's store
	// is the same — the directive must have landed.
	stateDir := expandHome(filepath.Dir(env.Config) + "/.nxd")
	contents, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !strings.Contains(string(contents), "USER_DIRECTIVE") {
		t.Errorf("USER_DIRECTIVE not in events.jsonl:\n%s", contents)
	}
	// Payload is base64-encoded JSON; decoding for a substring assert
	// would couple the test to internal serialization — the
	// USER_DIRECTIVE presence + the no-error return are sufficient
	// proof that the instruction flowed through.
}

// TestRunDirect_EmptyInstructionRejected guards against the silent
// case where an operator runs `nxd direct REQ-X` with no message — a
// no-op that would produce empty USER_DIRECTIVE events.
func TestRunDirect_EmptyInstructionRejected(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-EMPTY", "Test", "/tmp")
	cmd, _ := mkRunCmd(t, env.Config)
	cmd.Flags().String("message-file", "", "")

	err := runDirect(cmd, []string{"REQ-EMPTY"})
	if err == nil {
		t.Fatal("expected error for empty instruction")
	}
	if !strings.Contains(err.Error(), "empty instruction") {
		t.Errorf("error should mention empty instruction, got: %v", err)
	}
}

// TestRunDirect_UnknownIDFails confirms the resolver rejects IDs that
// don't match either a requirement or a story.
func TestRunDirect_UnknownIDFails(t *testing.T) {
	env := setupTestEnv(t)
	cmd, _ := mkRunCmd(t, env.Config)
	cmd.Flags().String("message-file", "", "")

	err := runDirect(cmd, []string{"NOT-A-REAL-ID", "do", "thing"})
	if err == nil {
		t.Fatal("expected error for unknown ID")
	}
}

// TestRunModelsCheck_OfflineWritesCache covers the offline path: with
// no Ollama or Google reachable, the checker still produces a (mostly
// empty) result and writes the cache file. Operators rely on the
// "No models to check" hint when running on a disconnected machine.
func TestRunModelsCheck_OfflineWritesCache(t *testing.T) {
	env := setupTestEnv(t)
	cmd, _ := mkRunCmd(t, env.Config)

	// Override Ollama host to a guaranteed-unreachable port so the
	// checker fails fast rather than waiting on its 3s timeout.
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")

	if err := runModelsCheck(cmd, nil); err != nil {
		t.Fatalf("runModelsCheck: %v", err)
	}

	stateDir := expandHome(filepath.Dir(env.Config) + "/.nxd")
	cachePath := filepath.Join(stateDir, "update-status.json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

// TestExecute_HelpReturnsNil covers the Execute() public entry. Cobra
// renders --help cleanly without touching any handler — locks down
// the root-cmd wiring so an init regression in root.go fails this
// test instead of being caught by users at runtime.
func TestExecute_HelpReturnsNil(t *testing.T) {
	prev := os.Args
	os.Args = []string{"nxd", "--help"}
	t.Cleanup(func() { os.Args = prev })

	r, w, _ := os.Pipe()
	prevOut := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = prevOut
	}()

	err := Execute()
	w.Close()

	var sink bytes.Buffer
	_, _ = sink.ReadFrom(r)
	if err != nil {
		t.Errorf("Execute --help: %v", err)
	}
}

// startFeedServer spins up an httptest server returning fixed bytes
// as application/json. Tests use it to drive runImprove's --feed
// branch without touching the network.
func startFeedServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

// TestRunDashboard_RejectsBadConfig covers the early-return path.
// runDashboard would otherwise attempt to start a Bubbletea TUI in
// the test process — by feeding a bad config path we make it bail
// out at loadStores before any TUI work.
func TestRunDashboard_RejectsBadConfig(t *testing.T) {
	cmd, _ := mkRunCmd(t, "/no/such/nxd.yaml")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().Bool("web", false, "")
	cmd.Flags().Int("port", 0, "")
	cmd.SetContext(context.Background())

	err := runDashboard(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

