package llm_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestClaudeCLIClient_ImplementsClientInterface(t *testing.T) {
	var _ llm.Client = llm.NewClaudeCLIClient()
}

func TestClaudeCLIClient_DefaultPath(t *testing.T) {
	client := llm.NewClaudeCLIClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestClaudeCLIClient_CustomPath(t *testing.T) {
	client := llm.NewClaudeCLIClientWithPath("/usr/local/bin/claude")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestClaudeCLIClient_MissingBinary(t *testing.T) {
	client := llm.NewClaudeCLIClientWithPath("nonexistent-binary-nxd-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Complete(ctx, llm.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		System:   "You are a test assistant.",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "claude CLI error") {
		t.Fatalf("expected 'claude CLI error' in message, got: %s", err.Error())
	}
}

func TestClaudeCLIClient_ContextCancellation(t *testing.T) {
	client := llm.NewClaudeCLIClientWithPath("sleep") // long-running binary

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}},
	})
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

// TestClaudeCLIClient_Integration is a smoke test that runs only when the
// claude CLI is available and authenticated. It verifies a simple completion
// round-trip. Skipped when the CLI is missing or not authenticated.
func TestClaudeCLIClient_Integration(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found on $PATH, skipping integration test")
	}

	client := llm.NewClaudeCLIClient()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		System:   "Respond with exactly one word: OK",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Respond now."}},
	})
	if err != nil {
		// Skip if the CLI is installed but not authenticated or has no credits.
		errMsg := err.Error()
		if strings.Contains(errMsg, "authentication") ||
			strings.Contains(errMsg, "unauthorized") ||
			strings.Contains(errMsg, "API key") ||
			strings.Contains(errMsg, "credit") ||
			strings.Contains(errMsg, "billing") {
			t.Skipf("claude CLI not authenticated, skipping integration test: %v", err)
		}
		t.Fatalf("integration complete: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("expected non-empty response content")
	}
}
