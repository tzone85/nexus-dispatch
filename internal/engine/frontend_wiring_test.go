package engine

import (
	"os"
	"strings"
	"testing"
)

// TestExecutor_WiresFrontendDetection guards against a dead-wire regression:
// the frontend design brief (agent.FrontendDesignBrief + PromptContext.
// IsFrontend) only fires if the executor actually calls detectFrontend and
// threads the flag into every prompt path — the CLI-runtime dispatch, the
// native-runtime dispatch, and retries via TemplateContext. The
// prompt-injection unit tests cannot catch the executor never setting the flag.
func TestExecutor_WiresFrontendDetection(t *testing.T) {
	src, err := os.ReadFile("executor.go")
	if err != nil {
		t.Fatalf("read executor.go: %v", err)
	}
	code := string(src)

	if got := strings.Count(code, "detectFrontend("); got < 2 {
		t.Errorf("executor.go must call detectFrontend on both the CLI and native dispatch paths, found %d call(s)", got)
	}
	if got := strings.Count(code, "IsFrontend:"); got < 3 {
		t.Errorf("executor.go must thread IsFrontend into both PromptContexts and the retry TemplateContext, found %d assignment(s)", got)
	}
}
