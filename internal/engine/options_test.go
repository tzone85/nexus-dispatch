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
