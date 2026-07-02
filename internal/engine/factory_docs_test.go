package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestBuildDocsIndex(t *testing.T) {
	idx := buildDocsIndex(
		[]string{"sequence.svg", "architecture.svg"}, // unsorted on purpose
		[]string{"training.md", "connectors.md"},
		true,
	)
	for _, want := range []string{
		"# Documentation",
		"../README.md",
		"[Architecture](architecture.svg)", // sorted + humanized
		"[Sequence](sequence.svg)",
		"[Training](training.md)",
		"[Connectors](connectors.md)",
		"Architecture Decision Records",
		"adr/README.md",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("docs index missing %q\n---\n%s", want, idx)
		}
	}
	// No ADRs → no ADR section.
	if strings.Contains(buildDocsIndex(nil, []string{"x.md"}, false), "Architecture Decision Records") {
		t.Error("ADR section should be absent when hasADRs is false")
	}
}

func TestEnsureDocsIndex_WritesFromDocsDir(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(filepath.Join(docs, "adr"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(docs, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("architecture.svg", "<svg/>")
	write("training.md", "# Training")
	write("adr/0001-x.md", "# 1. X")

	ensureDocsIndex(dir)

	got, err := os.ReadFile(filepath.Join(docs, "README.md"))
	if err != nil {
		t.Fatalf("docs/README.md not written: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "architecture.svg") || !strings.Contains(s, "training.md") || !strings.Contains(s, "adr/README.md") {
		t.Errorf("index missing entries:\n%s", s)
	}
}

func TestEnsureDocsIndex_NoDocsDirIsNoop(t *testing.T) {
	dir := t.TempDir()
	ensureDocsIndex(dir) // must not panic or create anything
	if _, err := os.Stat(filepath.Join(dir, "docs")); !os.IsNotExist(err) {
		t.Error("ensureDocsIndex should not create docs/ when absent")
	}
}

func TestHumanizeDocName(t *testing.T) {
	cases := map[string]string{
		"training.md":        "Training",
		"architecture.svg":   "Architecture",
		"getting-started.md": "Getting Started",
		"data_model.md":      "Data Model",
	}
	for in, want := range cases {
		if got := humanizeDocName(in); got != want {
			t.Errorf("humanizeDocName(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- ADRs --------------------------------------------------------------------

func TestParseADRs(t *testing.T) {
	raw := `Here are the ADRs:
[
  {"title": "Pure-Go SQLite", "context": "no cgo", "decision": "use modernc", "consequences": "easy builds"},
  {"title": "", "decision": "dropped — no title"},
  {"title": "No decision", "context": "x"},
  {"title": "Offline first", "context": "trust", "decision": "no network", "consequences": "portable"}
]`
	adrs := parseADRs(raw)
	if len(adrs) != 2 {
		t.Fatalf("expected 2 valid ADRs (title+decision required), got %d", len(adrs))
	}
	if adrs[0].Title != "Pure-Go SQLite" || adrs[1].Title != "Offline first" {
		t.Errorf("unexpected ADRs: %+v", adrs)
	}
}

func TestRenderADR_HasStandardSections(t *testing.T) {
	md := renderADR(2, adrRecord{Title: "Event sourcing", Context: "audit", Decision: "append-only log", Consequences: "replayable"})
	for _, want := range []string{"# 2. Event sourcing", "Status: Accepted", "## Context", "audit", "## Decision", "append-only log", "## Consequences", "replayable"} {
		if !strings.Contains(md, want) {
			t.Errorf("ADR markdown missing %q\n%s", want, md)
		}
	}
}

func TestSlugifyADR(t *testing.T) {
	cases := map[string]string{
		"Pure-Go SQLite (no cgo)": "pure-go-sqlite-no-cgo",
		"  Offline First!  ":      "offline-first",
		"":                        "decision",
	}
	for in, want := range cases {
		if got := slugifyADR(in); got != want {
			t.Errorf("slugifyADR(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnsureADRs_SkipsWhenAgentSupplied(t *testing.T) {
	dir := t.TempDir()
	adrDir := filepath.Join(dir, "docs", "adr")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adrDir, "0001-existing.md"), []byte("# 1. Existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Client with NO responses — if ensureADRs tried to call it, it would error;
	// the skip means it never does.
	client := llm.NewReplayClient()
	ensureADRs(context.Background(), dir, "proj", "tree", "{}", client, "m")
	if client.CallCount() != 0 {
		t.Errorf("ensureADRs should not call the model when ADRs already exist (calls=%d)", client.CallCount())
	}
}

func TestEnsureADRs_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	resp := llm.CompletionResponse{Content: `[
  {"title": "Layered architecture", "context": "separation", "decision": "domain/infra split", "consequences": "testable"},
  {"title": "Offline first", "context": "trust", "decision": "no network", "consequences": "portable"}
]`}
	client := llm.NewReplayClient(resp)
	ensureADRs(context.Background(), dir, "proj", "cmd/\ninternal/", "module x", client, "m")

	files, _ := os.ReadDir(filepath.Join(dir, "docs", "adr"))
	var adrCount int
	hasIndex := false
	for _, f := range files {
		if f.Name() == "README.md" {
			hasIndex = true
		} else if strings.HasSuffix(f.Name(), ".md") {
			adrCount++
		}
	}
	if adrCount != 2 {
		t.Errorf("expected 2 ADR files, got %d", adrCount)
	}
	if !hasIndex {
		t.Error("expected docs/adr/README.md index")
	}
}

func TestEnsureTrainingGuide_SkipsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "training.md"), []byte("# Existing guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := llm.NewReplayClient() // exhausted → would error if called
	ensureTrainingGuide(context.Background(), dir, "p", "t", "{}", client, "m")
	if client.CallCount() != 0 {
		t.Errorf("should not generate when training.md exists (calls=%d)", client.CallCount())
	}
}

func TestEnsureTrainingGuide_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	body := "# Getting Started\n\nInstall with `go build`. Run `./app`. Expected: it works and prints a result line."
	client := llm.NewReplayClient(llm.CompletionResponse{Content: body})
	ensureTrainingGuide(context.Background(), dir, "p", "tree", "{}", client, "m")
	got, err := os.ReadFile(filepath.Join(dir, "docs", "training.md"))
	if err != nil {
		t.Fatalf("training.md not written: %v", err)
	}
	if !strings.Contains(string(got), "Getting Started") {
		t.Errorf("unexpected training content: %s", got)
	}
}
