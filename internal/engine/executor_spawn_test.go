package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// captureRunner records the PreparedExecution a CLIRuntime hands to its
// backend instead of launching tmux.
type captureRunner struct {
	runs   []runtime.PreparedExecution
	runErr error
}

func (r *captureRunner) Run(pe runtime.PreparedExecution) error {
	r.runs = append(r.runs, pe)
	return r.runErr
}
func (r *captureRunner) Terminate(string) error                 { return nil }
func (r *captureRunner) SendInput(string, string) error         { return nil }
func (r *captureRunner) ReadOutput(string, int) (string, error) { return "", nil }
func (r *captureRunner) IsAlive(string) bool                    { return true }

// newCLISpawnExecutor wires an executor whose junior role resolves to the
// "aider" CLI runtime backed by a captureRunner.
func newCLISpawnExecutor(t *testing.T, es state.EventStore, ps state.ProjectionStore, stateDir string) (*Executor, *captureRunner) {
	t.Helper()
	runtimeCfg := map[string]config.RuntimeConfig{
		"aider": {Command: "true", Models: []string{"any-model"}},
	}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = stateDir
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "any-model"
	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := reg.Get("aider")
	if err != nil {
		t.Fatal(err)
	}
	cli, ok := rt.(*runtime.CLIRuntime)
	if !ok {
		t.Fatalf("aider runtime is %T, want *runtime.CLIRuntime", rt)
	}
	runner := &captureRunner{}
	cli.WithRunner(runner)
	return NewExecutor(reg, cfg, es, ps, nil), runner
}

func spawnAssignment(story string) Assignment {
	return Assignment{
		StoryID: story, ReqID: "REQ-SP", Role: agent.RoleJunior, Branch: "nxd/" + story,
		AgentID: "junior-001", SessionName: "nxd-" + story, AttemptID: story + "-a01HZX0000000000000000000B",
	}
}

func TestSpawn_RetryPromptCarriesFeedbackAndAttemptHistory(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := pipelineStores(t)
	seedCapacityStory(t, es, ps, "REQ-SP", "s-retry")
	e, runner := newCLISpawnExecutor(t, es, ps, t.TempDir())
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	e.SetController(ctrl)

	// A prior attempt that was rejected by the reviewer.
	for _, evt := range []state.Event{
		state.NewEventForAttempt(state.EventStoryStarted, "junior-000", "s-retry", "s-retry-a01HZX0000000000000000000A", map[string]any{
			"runtime": "aider", "tier": 0, "role": "junior",
		}),
		state.NewEventForAttempt(state.EventStoryReviewFailed, "reviewer", "s-retry", "s-retry-a01HZX0000000000000000000A", map[string]any{
			"reason": "review rejected: handler has no tests",
		}),
	} {
		if err := es.Append(evt); err != nil {
			t.Fatal(err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatal(err)
		}
	}

	a := spawnAssignment("s-retry")
	story := PlannedStory{ID: "s-retry", Title: "Add handler", Description: "HTTP handler", AcceptanceCriteria: "handler tested", Complexity: 2}
	res := e.spawn(context.Background(), repo, a, story, []WaveStoryInfo{{ID: "s-other", Title: "Other", OwnedFiles: []string{"other.go"}}}, nil)
	if res.Error != nil {
		t.Fatalf("spawn: %v", res.Error)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runner runs = %d, want 1", len(runner.runs))
	}
	pe := runner.runs[0]
	prompt := pe.SetupFiles[filepath.Join(pe.WorkDir, runtime.PromptFileRel)]
	for _, want := range []string{"handler has no tests", "Prior Attempts", "Attempt 1 (junior)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("retry prompt lacks %q", want)
		}
	}
	if pe.SessionName != a.SessionName || pe.WorkDir != res.WorktreePath {
		t.Errorf("prepared execution = %+v", pe)
	}
	if !strings.Contains(pe.Command, "--model 'any-model'") && !strings.Contains(pe.Command, "--model any-model") {
		t.Errorf("command does not pass the role model: %q", pe.Command)
	}

	started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted, StoryID: "s-retry"})
	if len(started) != 2 {
		t.Fatalf("STORY_STARTED = %d, want 2 (prior + this attempt)", len(started))
	}
	latest := started[1]
	if latest.AttemptID != a.AttemptID {
		t.Errorf("attempt_id = %q, want %q", latest.AttemptID, a.AttemptID)
	}
	p := state.DecodePayload(latest.Payload)
	if p["runtime"] != "aider" || p["role"] != "junior" || p["tier"].(float64) != 0 || p["session_name"] != a.SessionName {
		t.Errorf("STORY_STARTED payload = %v", p)
	}
	// The tmux session was handed to the controller so a cancel can kill it.
	ctrl.mu.Lock()
	sess, registered := ctrl.sessions["s-retry"]
	ctrl.mu.Unlock()
	if !registered || sess.sessionName != a.SessionName {
		t.Errorf("controller session = %+v registered=%v", sess, registered)
	}
}

func TestSpawn_RunnerFailureIsReported(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := pipelineStores(t)
	e, runner := newCLISpawnExecutor(t, es, ps, t.TempDir())
	runner.runErr = errors.New("tmux: server not running")

	res := e.spawn(context.Background(), repo, spawnAssignment("s-fail"), PlannedStory{ID: "s-fail", Title: "T"}, nil, nil)
	if res.Error == nil || !strings.Contains(res.Error.Error(), "spawn runtime for s-fail") || !strings.Contains(res.Error.Error(), "tmux: server not running") {
		t.Fatalf("error = %v", res.Error)
	}
	if started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted}); len(started) != 0 {
		t.Errorf("STORY_STARTED must not be emitted when the runtime failed to start, got %d", len(started))
	}
}

// newNativeSpawnExecutor wires a native "gemma" runtime for the junior role.
func newNativeSpawnExecutor(t *testing.T, es state.EventStore, ps state.ProjectionStore, client llm.Client) *Executor {
	t.Helper()
	runtimeCfg := map[string]config.RuntimeConfig{
		"gemma": {Native: true, Models: []string{"gemma4"}, MaxIterations: 3, Concurrency: 1},
	}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = t.TempDir()
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "gemma4:e4b"
	cfg.QA.SuccessCriteria = nil
	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(reg, cfg, es, ps, nil)
	if client != nil {
		e.SetLLMClient(client)
	}
	return e
}

func TestSpawnAll_NativeRuntimeRunsToCompletion(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := pipelineStores(t)
	seedCapacityStory(t, es, ps, "REQ-SP", "s-native")
	client := llm.NewReplayClient(llm.CompletionResponse{Content: "Nothing to do, the code already satisfies the story."})
	e := newNativeSpawnExecutor(t, es, ps, client)
	store, err := artifact.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.SetArtifactStore(store)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	e.SetController(ctrl)

	a := spawnAssignment("s-native")
	stories := map[string]PlannedStory{"s-native": {ID: "s-native", Title: "Native task", Description: "d", Complexity: 1}}
	results := e.SpawnAll(context.Background(), repo, []Assignment{a}, stories)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("results = %+v", results)
	}
	if results[0].RuntimeName != "gemma" {
		t.Errorf("runtime = %q, want gemma", results[0].RuntimeName)
	}
	if err := e.WaitForNativeShutdown(10 * time.Second); err != nil {
		t.Fatalf("native goroutine did not finish: %v", err)
	}

	started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted, StoryID: "s-native"})
	if len(started) != 1 || started[0].AttemptID != a.AttemptID {
		t.Fatalf("STORY_STARTED = %+v", started)
	}
	if p := state.DecodePayload(started[0].Payload); p["runtime"] != "gemma" || p["role"] != "junior" {
		t.Errorf("STORY_STARTED payload = %v", p)
	}
	completed, _ := es.List(state.EventFilter{Type: state.EventStoryCompleted, StoryID: "s-native"})
	if len(completed) != 1 || completed[0].AttemptID != a.AttemptID {
		t.Fatalf("STORY_COMPLETED = %+v", completed)
	}
	cp := state.DecodePayload(completed[0].Payload)
	if cp["native"] != true || cp["iterations"].(float64) != 1 || !strings.Contains(cp["summary"].(string), "Nothing to do") {
		t.Errorf("STORY_COMPLETED payload = %v", cp)
	}
	if _, hasErr := cp["error"]; hasErr {
		t.Errorf("successful run must not record an error: %v", cp)
	}
	progress, _ := es.List(state.EventFilter{Type: state.EventStoryProgress, StoryID: "s-native"})
	if len(progress) == 0 {
		t.Fatal("expected STORY_PROGRESS events from the native loop")
	}
	if progress[0].AttemptID != a.AttemptID {
		t.Errorf("progress attempt_id = %q", progress[0].AttemptID)
	}
	var execOutcome string
	stages, _ := es.List(state.EventFilter{Type: state.EventStageCompleted, StoryID: "s-native"})
	for _, evt := range stages {
		if p := state.DecodePayload(evt.Payload); p["stage"] == "execute" {
			execOutcome, _ = p["outcome"].(string)
		}
	}
	if execOutcome != "success" {
		t.Errorf("execute stage outcome = %q, want success", execOutcome)
	}
	launch, err := store.Read("s-native", string(artifact.TypeLaunchConfig)+".json")
	if err != nil || !strings.Contains(string(launch), `"gemma4:e4b"`) {
		t.Errorf("launch config artifact = %s (err=%v)", launch, err)
	}
	if trace, err := store.Read("s-native", string(artifact.TypeTraceEvents)+".jsonl"); err != nil || len(trace) == 0 {
		t.Errorf("trace artifact missing: %v", err)
	}
	// The cancel handle was released when the run finished.
	ctrl.mu.Lock()
	_, stillRegistered := ctrl.cancelFuncs["s-native"]
	ctrl.mu.Unlock()
	if stillRegistered {
		t.Error("controller cancel func must be deregistered after completion")
	}
	if st, err := ps.GetStory("s-native"); err != nil || st.Status != "review" {
		t.Errorf("story status = %q (err=%v), want review", st.Status, err)
	}
}

func TestSpawnAll_NativeRuntimeRecordsLLMFailure(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := pipelineStores(t)
	seedCapacityStory(t, es, ps, "REQ-SP", "s-native-err")
	e := newNativeSpawnExecutor(t, es, ps, llm.NewErrorClient(errors.New("model not found")))

	a := spawnAssignment("s-native-err")
	results := e.SpawnAll(context.Background(), repo, []Assignment{a}, map[string]PlannedStory{"s-native-err": {ID: "s-native-err", Title: "T"}})
	if results[0].Error != nil {
		t.Fatalf("spawn error: %v", results[0].Error)
	}
	if err := e.WaitForNativeShutdown(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	completed, _ := es.List(state.EventFilter{Type: state.EventStoryCompleted, StoryID: "s-native-err"})
	if len(completed) != 1 {
		t.Fatalf("STORY_COMPLETED = %d, want 1 (failures still complete)", len(completed))
	}
	cp := state.DecodePayload(completed[0].Payload)
	if errText, _ := cp["error"].(string); !strings.Contains(errText, "model not found") {
		t.Errorf("STORY_COMPLETED error = %v", cp["error"])
	}
	var execOutcome string
	stages, _ := es.List(state.EventFilter{Type: state.EventStageCompleted, StoryID: "s-native-err"})
	for _, evt := range stages {
		if p := state.DecodePayload(evt.Payload); p["stage"] == "execute" {
			execOutcome, _ = p["outcome"].(string)
		}
	}
	if execOutcome != "failure" {
		t.Errorf("execute stage outcome = %q, want failure", execOutcome)
	}
}

func TestSpawn_NativeWithoutLLMClientFails(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := pipelineStores(t)
	e := newNativeSpawnExecutor(t, es, ps, nil)

	results := e.SpawnAll(context.Background(), repo, []Assignment{spawnAssignment("s-nollm")}, map[string]PlannedStory{})
	if len(results) != 1 || results[0].Error == nil || !strings.Contains(results[0].Error.Error(), "requires an LLM client") {
		t.Fatalf("results = %+v", results)
	}
	if started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted}); len(started) != 0 {
		t.Errorf("STORY_STARTED emitted without a runnable agent: %d", len(started))
	}
}
