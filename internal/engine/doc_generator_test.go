package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t.t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}
}

// generateDocumentation must, after writing the README, deterministically
// produce VALID SVG diagrams and link them — the factory documentation loop end
// to end. This is the wiring guard: if the diagram call is ever dropped from
// generateDocumentation, this fails.
func TestGenerateDocumentation_ProducesSVGDiagrams(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The post-merge doc loop makes, in order: README, architecture.svg,
	// sequence.svg, training.md, then the ADR list. The docs index is
	// deterministic (no LLM call).
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "# Demo\n\nA demo project."},
		llm.CompletionResponse{Content: validArchSVG},
		llm.CompletionResponse{Content: validArchSVG},
		llm.CompletionResponse{Content: "# Getting Started\n\nRun `go build` to compile, then `./demo` to run it. Expected output: a single result line confirming success."},
		llm.CompletionResponse{Content: `[{"title":"Layered design","context":"separation","decision":"domain/infra split","consequences":"testable"}]`},
	)

	generateDocumentation(context.Background(), dir, "Build a demo", []string{"s-001: thing"}, client, "m")

	for _, rel := range []string{"docs/architecture.svg", "docs/sequence.svg"} {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
		if err := validateSVG(string(data)); err != nil {
			t.Fatalf("%s not valid SVG: %v", rel, err)
		}
	}

	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "docs/architecture.svg") {
		t.Fatal("README does not reference the architecture diagram")
	}

	// The full factory documentation set must exist: training guide, at least
	// one ADR + its index, and the docs index.
	for _, rel := range []string{"docs/training.md", "docs/adr/README.md", "docs/README.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s to be generated: %v", rel, err)
		}
	}
	adrs, _ := filepath.Glob(filepath.Join(dir, "docs/adr/0*.md"))
	if len(adrs) == 0 {
		t.Error("expected at least one numbered ADR file")
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "docs/README.md"))
	if !strings.Contains(string(idx), "training.md") || !strings.Contains(string(idx), "adr/README.md") {
		t.Errorf("docs index does not link the generated docs:\n%s", idx)
	}

	// Everything must be committed, not left dirty.
	st := exec.Command("git", "status", "--porcelain")
	st.Dir = dir
	out, _ := st.Output()
	if strings.Contains(string(out), "docs/") {
		t.Fatalf("docs left uncommitted: %s", out)
	}
}
