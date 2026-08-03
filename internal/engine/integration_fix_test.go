package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// recordingFixClient captures whether Complete ran and whether its context was
// already cancelled when it did.
type recordingFixClient struct {
	called chan struct{}
	ctxErr error
}

func (c *recordingFixClient) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	c.ctxErr = ctx.Err()
	close(c.called)
	return llm.CompletionResponse{Content: "reconcile handler.Handler signature"}, nil
}

// TestDispatchIntegrationFix_SurvivesCallerContextCancel proves the detached
// diagnosis goroutine is NOT killed when the caller (postExecutionPipeline)
// returns and cancels its pipeline context. Before the fix, fixCtx was a child
// of the caller's context, so the LLM call observed context.Canceled and the
// fix hint was never produced.
func TestDispatchIntegrationFix_SurvivesCallerContextCancel(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int-1")

	client := &recordingFixClient{called: make(chan struct{})}
	fixer := NewTechLeadFixer(client, "model", 256, es, ps)

	// Mirror the real call site: pass a cancellable context, then cancel it the
	// instant DispatchIntegrationFix returns (as the deferred pipelineCancel does).
	ctx, cancel := context.WithCancel(context.Background())
	fixer.DispatchIntegrationFix(ctx, "s-int-1", t.TempDir(), "build broke")
	cancel()

	select {
	case <-client.called:
		if client.ctxErr != nil {
			t.Fatalf("LLM call ran with an already-cancelled context: %v", client.ctxErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("integration-fix LLM call never ran — goroutine was cancelled with the caller context")
	}
}

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
