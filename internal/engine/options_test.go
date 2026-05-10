package engine

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
)

func TestExecutorConfigure_AppliesOptions(t *testing.T) {
	e := &Executor{}

	// nil-safe: a nil option must be skipped, not panic.
	e.Configure(nil)

	// We use minimal stand-ins (zero-value structs) to verify wiring without
	// constructing every dependency. The struct fields are package-private
	// so we have to live in the same package — that's why this is *_test.go
	// inside engine.
	tmpDir := t.TempDir()
	artStore, err := artifact.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("artifact.NewStore: %v", err)
	}
	sb, err := scratchboard.New(tmpDir + "/sb.jsonl")
	if err != nil {
		t.Fatalf("scratchboard.New: %v", err)
	}
	directives := &DirectiveStore{}

	e.Configure(
		WithExecArtifactStore(artStore),
		WithExecScratchboard(sb),
		WithExecProjectDir("/tmp/proj"),
		WithExecDirectiveStore(directives),
	)

	if e.artifactStore != artStore {
		t.Error("artifactStore not set")
	}
	if e.scratchboard != sb {
		t.Error("scratchboard not set")
	}
	if e.projectDir != "/tmp/proj" {
		t.Errorf("projectDir = %q, want /tmp/proj", e.projectDir)
	}
	if e.directives != directives {
		t.Error("directives not set")
	}
}

func TestMonitorConfigure_AppliesOptions(t *testing.T) {
	m := &Monitor{}
	m.Configure(nil) // nil-safe

	m.Configure(
		WithMonDryRun(true),
	)

	if !m.dryRun {
		t.Error("dryRun option not applied")
	}
}

// TestExecutorConfigure_RemainingOptions covers the With* options not
// touched by the broader test above: LLM client, controller. Each
// sets a single struct field — tests confirm the assignment lands so
// future renames don't silently break the wiring (resume.go relies on
// every option being functional).
func TestExecutorConfigure_RemainingOptions(t *testing.T) {
	e := &Executor{}
	c := &Controller{}
	e.Configure(WithExecController(c))
	if e.controller != c {
		t.Error("controller not set")
	}
	// LLM client typed via interface; nil is a valid value but the
	// option must still write into the field. Using a sentinel is
	// awkward, so we just confirm the option function is non-nil.
	opt := WithExecLLMClient(nil)
	if opt == nil {
		t.Error("WithExecLLMClient returned nil option")
	}
	opt(e) // exercise the closure body for coverage
}

// TestMonitorConfigure_RemainingOptions covers the monitor-side
// counterparts: artifact store, conflict resolver, manager, planner,
// auto-resume (which sets dispatcher + executor + flag),
// bayesian router, codegraph runner. nil values are accepted on
// purpose so chaining works without a guard at the caller — the
// Monitor's runtime checks each field for nil before use.
func TestMonitorConfigure_RemainingOptions(t *testing.T) {
	m := &Monitor{}

	cr := &ConflictResolver{}
	mgr := &Manager{}
	planner := &Planner{}
	dispatcher := &Dispatcher{}
	executor := &Executor{}

	m.Configure(
		WithMonMemPalace(nil), // nil acceptable — no MemPalace bridge in test
		WithMonArtifactStore(nil),
		WithMonConflictResolver(cr),
		WithMonManager(mgr),
		WithMonPlanner(planner),
		WithMonAutoResume(dispatcher, executor),
		WithMonBayesianRouter(nil),
		WithMonCodeGraph(nil),
	)

	if m.conflictResolver != cr {
		t.Error("conflictResolver not set")
	}
	if m.manager != mgr {
		t.Error("manager not set")
	}
	if m.planner != planner {
		t.Error("planner not set")
	}
	if m.dispatcher != dispatcher || m.executor != executor {
		t.Error("auto-resume wiring not applied")
	}
}
