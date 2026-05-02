package cli

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// TestLogNoticesOnce_DedupesAcrossLoadConfigCalls is the regression
// test for the same-model triple-print bug. Before the fix, each call
// to loadConfig (PersistentPreRun, the command runner, plus any
// explicit Validate call) re-emitted the same WARNING, so users saw
// the line 2-3 times per command. logNoticesOnce now keeps a
// process-scoped seen set so each unique notice prints exactly once.
func TestLogNoticesOnce_DedupesAcrossLoadConfigCalls(t *testing.T) {
	// Reset the global dedupe state so this test is isolated.
	noticesMu.Lock()
	noticesPrinted = map[string]struct{}{}
	noticesMu.Unlock()

	// Capture log output (the helper writes via the stdlib log
	// package, which is bridged through nlog in normal operation).
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	prevFlags := log.Flags()
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	}()

	// Build a config that triggers the same-model notice (junior +
	// intermediate both equal senior).
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
workspace:
  state_dir: `+dir+`
models:
  tech_lead:
    provider: ollama
    model: shared
    max_tokens: 1000
  senior:
    provider: ollama
    model: shared
    max_tokens: 1000
  junior:
    provider: ollama
    model: shared
    max_tokens: 1000
  intermediate:
    provider: ollama
    model: shared
    max_tokens: 1000
  investigator:
    provider: ollama
    model: shared
    max_tokens: 1000
`), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := loadConfig(cfgPath); err != nil {
			t.Fatalf("loadConfig #%d: %v", i, err)
		}
	}

	out := buf.String()
	juniorCount := strings.Count(out, "models.junior.model")
	intermediateCount := strings.Count(out, "models.intermediate.model")

	if juniorCount != 1 {
		t.Errorf("junior notice printed %d times, want 1; output:\n%s", juniorCount, out)
	}
	if intermediateCount != 1 {
		t.Errorf("intermediate notice printed %d times, want 1; output:\n%s", intermediateCount, out)
	}
}

// TestLogNoticesOnce_NewNoticeStillPrints confirms the dedupe doesn't
// silence genuinely-new notices. After we record one notice, a
// different notice on a fresh config still surfaces.
func TestLogNoticesOnce_NewNoticeStillPrints(t *testing.T) {
	noticesMu.Lock()
	noticesPrinted = map[string]struct{}{}
	noticesMu.Unlock()

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	prevFlags := log.Flags()
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	}()

	cfg1 := config.DefaultConfig()
	cfg1.Models.Senior.Model = "modelA"
	cfg1.Models.Junior.Model = "modelA"
	logNoticesOnce(cfg1)

	cfg2 := config.DefaultConfig()
	cfg2.Models.Senior.Model = "modelB"
	cfg2.Models.Intermediate.Model = "modelB"
	logNoticesOnce(cfg2)

	out := buf.String()
	if !strings.Contains(out, "models.junior.model") {
		t.Errorf("missing junior notice in: %s", out)
	}
	if !strings.Contains(out, "models.intermediate.model") {
		t.Errorf("missing intermediate notice in: %s", out)
	}
}
