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
