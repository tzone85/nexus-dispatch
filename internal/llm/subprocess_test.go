package llm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// writeTempScript creates an executable bash script in a temp directory
// and returns its path. The caller does not need to clean up; t.TempDir
// handles removal automatically.
func writeTempScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	content := "#!/bin/bash\n" + body
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write temp script: %v", err)
	}
	return path
}

func TestSubprocessClient_Complete(t *testing.T) {
	script := writeTempScript(t, "mock_provider.sh", `
read input
cat <<'RESP'
{"content":"hello from subprocess","model":"custom-model","stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}
RESP
`)

	client := llm.NewSubprocessClient(script, 5*time.Second)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "custom-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello from subprocess" {
		t.Fatalf("expected content 'hello from subprocess', got %q", resp.Content)
	}
	if resp.Model != "custom-model" {
		t.Fatalf("expected model 'custom-model', got %q", resp.Model)
	}
	if resp.StopReason != "end" {
		t.Fatalf("expected stop_reason 'end', got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Fatalf("expected 10 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Fatalf("expected 5 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

func TestSubprocessClient_ScriptError(t *testing.T) {
	script := writeTempScript(t, "fail_provider.sh", `
read input
exit 1
`)

	client := llm.NewSubprocessClient(script, 5*time.Second)

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "test",
	})
	if err == nil {
		t.Fatal("expected error from failing script")
	}
	if !strings.Contains(err.Error(), "subprocess provider") {
		t.Fatalf("expected 'subprocess provider' in error, got %q", err.Error())
	}
}

func TestSubprocessClient_InvalidJSON(t *testing.T) {
	script := writeTempScript(t, "bad_json_provider.sh", `
read input
echo "this is not json"
`)

	client := llm.NewSubprocessClient(script, 5*time.Second)

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "test",
	})
	if err == nil {
		t.Fatal("expected error from invalid JSON response")
	}
	if !strings.Contains(err.Error(), "parse subprocess response") {
		t.Fatalf("expected 'parse subprocess response' in error, got %q", err.Error())
	}
}

func TestSubprocessClient_ImplementsInterface(t *testing.T) {
	var _ llm.Client = llm.NewSubprocessClient("echo", time.Second)
}
