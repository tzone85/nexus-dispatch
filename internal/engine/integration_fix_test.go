package engine

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestTechLeadFixer_BuildPrompt_ContainsRequiredSections verifies that the
// prompt produced by buildPrompt contains the build error, a stories section,
// and instruction to produce a fix story.
func TestTechLeadFixer_BuildPrompt_ContainsRequiredSections(t *testing.T) {
	fixer := &TechLeadFixer{model: "qwen3-coder:30b"}

	stories := []state.Story{
		{ID: "abc12345-s-001", Title: "Add HTTP handler"},
		{ID: "abc12345-s-002", Title: "Wire handler to server"},
	}
	buildErr := "cmd/server/main.go:12:15: undefined: handler.Handler"

	prompt := fixer.buildPrompt("abc12345-s-002", buildErr, stories)

	// Must contain the build error.
	if !strings.Contains(prompt, buildErr) {
		t.Errorf("prompt missing build error\ngot:\n%s", prompt)
	}
	// Must list recently merged stories.
	if !strings.Contains(prompt, "Add HTTP handler") {
		t.Errorf("prompt missing story title 'Add HTTP handler'")
	}
	if !strings.Contains(prompt, "Wire handler to server") {
		t.Errorf("prompt missing story title 'Wire handler to server'")
	}
	// Must ask for a fix story.
	if !strings.Contains(prompt, "fix") && !strings.Contains(prompt, "reconcil") {
		t.Errorf("prompt does not ask for fix/reconciliation")
	}
}

// TestTechLeadFixer_BuildPrompt_EmptyStories verifies that buildPrompt handles
// an empty stories slice without panicking.
func TestTechLeadFixer_BuildPrompt_EmptyStories(t *testing.T) {
	fixer := &TechLeadFixer{model: "qwen3-coder:30b"}
	prompt := fixer.buildPrompt("story-001", "build failed", nil)
	if len(prompt) == 0 {
		t.Error("expected non-empty prompt even with no stories")
	}
}

// TestTechLeadFixer_BuildPrompt_NXDLogsHint verifies that buildPrompt
// references nxd (not vxd) for follow-up instructions.
func TestTechLeadFixer_BuildPrompt_NXDLogsHint(t *testing.T) {
	fixer := &TechLeadFixer{model: "qwen3-coder:30b"}
	prompt := fixer.buildPrompt("story-001", "some build error", nil)
	// Prompt should not reference "vxd" — that's the cloud version.
	if strings.Contains(prompt, "vxd req") {
		t.Errorf("prompt references 'vxd req' — should reference 'nxd req' for the offline version")
	}
}
