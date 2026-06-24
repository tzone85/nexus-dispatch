package cli

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestRunReviewStory_ShowsIntent verifies that `nxd review <story>` — the path a
// human takes to "click through" a story — surfaces the description and the
// acceptance criteria as readable bullet items, so the intent is legible.
func TestRunReviewStory_ShowsIntent(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-rvi", "Review Intent", env.Dir)

	evt := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-rvi1", map[string]any{
		"id":                  "s-rvi1",
		"req_id":              "req-rvi",
		"title":               "Domain model",
		"complexity":          3,
		"description":         "Define the core domain entities for the world.",
		"acceptance_criteria": "Failing tests written first. go test green. WorldState.copy() produces independent instance.",
	})
	env.Events.Append(evt)
	env.Proj.Project(evt)

	cmd := newReviewStoryCmd()
	out, err := execCmd(t, cmd, env.Config, "s-rvi1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Description:") {
		t.Errorf("expected a Description: label, got:\n%s", out)
	}
	if !strings.Contains(out, "Define the core domain entities for the world.") {
		t.Errorf("expected the description text, got:\n%s", out)
	}
	if !strings.Contains(out, "Acceptance Criteria:") {
		t.Errorf("expected an Acceptance Criteria: label, got:\n%s", out)
	}
	// Each criterion should appear as its own bullet, not a run-on blob.
	for _, want := range []string{
		"- Failing tests written first.",
		"- go test green.",
		"- WorldState.copy() produces independent instance.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected bullet %q in output, got:\n%s", want, out)
		}
	}
}
