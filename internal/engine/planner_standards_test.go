package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestPlanner_PromptIncludesFactoryStandards pins the standards blocks in the
// decomposition prompt: the security baseline (a gate runs on every story) and
// the frontend design standards (token-first foundation story, no default
// fonts, accessibility quality floor in acceptance criteria).
func TestPlanner_PromptIncludesFactoryStandards(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	defer ps.Close()

	resp := `[{"id":"s-001","title":"A","description":"d","acceptance_criteria":"ac","complexity":3,"depends_on":[]}]`
	client := llm.NewReplayClient(llm.CompletionResponse{Content: resp})
	planner := engine.NewPlanner(client, config.DefaultConfig(), es, ps)

	if _, err := planner.Plan(context.Background(), "r-001", "Build a web app", dir); err != nil {
		t.Fatalf("plan: %v", err)
	}

	prompt := client.CallAt(0).Messages[0].Content
	for _, must := range []string{"OWASP", "SSRF"} {
		if !strings.Contains(prompt, must) {
			t.Errorf("decomposition prompt missing security standard %q", must)
		}
	}
	for _, must := range []string{"design-token", "Inter", "WCAG", "prefers-reduced-motion", "360px"} {
		if !strings.Contains(prompt, must) {
			t.Errorf("decomposition prompt missing frontend design standard %q", must)
		}
	}
}

// TestPlanner_EmitsIntegrationAndScribeStories pins the two factory stories
// appended to every persisted plan: the integration story (wire everything
// into the real entry point + smoke test) and the scribe story (README +
// training + SVG diagrams + ADRs + docs index), both depending on every other
// story so they run last.
func TestPlanner_EmitsIntegrationAndScribeStories(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	defer ps.Close()

	resp := `[{"id":"s-001","title":"A","description":"d","acceptance_criteria":"ac","complexity":3,"depends_on":[]}]`
	// Two responses: one for the persisted plan, one for the ephemeral estimate.
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: resp},
		llm.CompletionResponse{Content: resp},
	)
	planner := engine.NewPlanner(client, config.DefaultConfig(), es, ps)

	result, err := planner.Plan(context.Background(), "r-001", "Build a web app", dir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Stories) != 3 {
		t.Fatalf("want code story + integration + scribe (3), got %d", len(result.Stories))
	}

	integrate := result.Stories[1]
	if !strings.Contains(integrate.ID, "integrate") {
		t.Errorf("second appended story should be the integration story, got %s", integrate.ID)
	}
	if !strings.Contains(integrate.Description, "SMOKE TEST") {
		t.Error("integration story must demand an end-to-end smoke test")
	}

	scribe := result.Stories[2]
	if !strings.Contains(scribe.ID, "scribe-readme") {
		t.Errorf("final story should be the scribe story, got %s", scribe.ID)
	}
	for _, want := range []string{"docs/architecture.svg", "docs/sequence.svg", "docs/training.md", "docs/README.md"} {
		found := false
		for _, f := range scribe.OwnedFiles {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("scribe story must own %s, got %v", want, scribe.OwnedFiles)
		}
	}
	// Both must depend on the code story so they run last.
	for _, s := range []engine.PlannedStory{integrate, scribe} {
		if len(s.DependsOn) == 0 {
			t.Errorf("%s must depend on every other story", s.ID)
		}
	}
	// Estimates (ephemeral plans) must NOT include the appended stories.
	est, err := planner.PlanEphemeral(context.Background(), "est-1", "Build a web app", dir)
	if err != nil {
		t.Fatalf("ephemeral plan: %v", err)
	}
	if len(est.Stories) != 1 {
		t.Errorf("ephemeral plan must not append factory stories, got %d", len(est.Stories))
	}
}
