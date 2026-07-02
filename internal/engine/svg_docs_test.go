package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

const validArchSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100" width="200" height="100">` +
	`<rect x="10" y="10" width="80" height="40" fill="#eef"/>` +
	`<text x="20" y="35">CLI</text>` +
	`<line x1="90" y1="30" x2="150" y2="30" stroke="#333"/>` +
	`</svg>`

func TestValidateSVG(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid svg", validArchSVG, false},
		{"valid with xml decl", `<?xml version="1.0"?>` + validArchSVG, false},
		{"empty", "   ", true},
		{"mermaid fence", "```mermaid\ngraph TD\nA-->B\n```", true},
		{"raw mermaid", "sequenceDiagram\nA->>B: hi", true},
		{"flowchart keyword", "flowchart LR\nA-->B", true},
		{"plain prose", "Here is your architecture diagram.", true},
		{"svg without xmlns", `<svg viewBox="0 0 10 10"><rect/></svg>`, true},
		{"malformed xml", `<svg xmlns="http://www.w3.org/2000/svg"><rect></svg>`, true},
		{"non-svg root", `<?xml version="1.0"?><html xmlns="x"><svg></svg></html>`, true},
		{"contains code fence", "```\n" + validArchSVG + "\n```", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSVG(c.in)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

func TestExtractSVG(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare", validArchSVG, validArchSVG},
		{"fenced", "```xml\n" + validArchSVG + "\n```", validArchSVG},
		{"prose around", "Sure! Here:\n" + validArchSVG + "\nHope that helps.", validArchSVG},
		{"no svg", "no diagram here", "no diagram here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractSVG(c.in); got != c.want {
				t.Fatalf("extractSVG = %q, want %q", got, c.want)
			}
		})
	}
}

// The retry loop must reject an invalid (Mermaid) first response, feed the
// error back, and accept the corrected SVG on the next attempt.
func TestGenerateSVGDiagram_RetriesUntilValid(t *testing.T) {
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "```mermaid\ngraph TD\nA-->B\n```"},
		llm.CompletionResponse{Content: validArchSVG},
	)
	got, err := generateSVGDiagram(context.Background(), client, "m", factoryDiagrams[0], "vatkit", "{}", "cmd/\ninternal/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != validArchSVG {
		t.Fatalf("got %q", got)
	}
	if client.CallCount() != 2 {
		t.Fatalf("expected 2 attempts, got %d", client.CallCount())
	}
	// The corrective re-prompt must carry the validation failure back.
	if !strings.Contains(client.CallAt(1).Messages[0].Content, "REJECTED") {
		t.Fatal("retry prompt did not feed the validation error back")
	}
}

func TestGenerateSVGDiagram_ExhaustsAttempts(t *testing.T) {
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "```mermaid\nA"},
		llm.CompletionResponse{Content: "still mermaid graph TD"},
		llm.CompletionResponse{Content: "nope"},
	)
	if _, err := generateSVGDiagram(context.Background(), client, "m", factoryDiagrams[1], "x", "{}", ""); err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
}

func TestGenerateProjectDiagrams_WritesValidFiles(t *testing.T) {
	dir := t.TempDir()
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: validArchSVG},
		llm.CompletionResponse{Content: validArchSVG},
	)
	written := generateProjectDiagrams(context.Background(), dir, "proj", "tree", "{}", client, "m")
	if len(written) != 2 {
		t.Fatalf("expected 2 diagrams written, got %d (%v)", len(written), written)
	}
	for _, rel := range []string{"docs/architecture.svg", "docs/sequence.svg"} {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if err := validateSVG(string(data)); err != nil {
			t.Fatalf("%s not valid svg: %v", rel, err)
		}
	}
}

// A pre-existing VALID svg must be left untouched (no LLM call consumed).
func TestGenerateProjectDiagrams_KeepsExistingValid(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs/architecture.svg"), []byte(validArchSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only ONE response available — enough for sequence.svg only. If the loop
	// tried to regenerate the existing valid architecture.svg it would exhaust
	// the client and fail to write sequence.svg.
	client := llm.NewReplayClient(llm.CompletionResponse{Content: validArchSVG})
	written := generateProjectDiagrams(context.Background(), dir, "proj", "tree", "{}", client, "m")
	if len(written) != 1 || written[0] != "docs/sequence.svg" {
		t.Fatalf("expected only sequence.svg regenerated, got %v", written)
	}
}

func TestEnsureReadmeReferencesDiagrams(t *testing.T) {
	written := []string{"docs/architecture.svg", "docs/sequence.svg"}

	got := ensureReadmeReferencesDiagrams("# Title\n\nSome prose.", written)
	if !strings.Contains(got, "docs/architecture.svg") || !strings.Contains(got, "docs/sequence.svg") {
		t.Fatal("expected diagram links appended")
	}

	already := "# Title\n\n![Architecture](docs/architecture.svg)\n"
	if got := ensureReadmeReferencesDiagrams(already, written); got != already {
		t.Fatal("must not duplicate when already referenced")
	}

	if got := ensureReadmeReferencesDiagrams("# Title", nil); got != "# Title" {
		t.Fatal("no diagrams written → README unchanged")
	}
}
