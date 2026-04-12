package repolearn

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestScanDeep_NilClient(t *testing.T) {
	profile := &RepoProfile{RepoPath: t.TempDir()}
	err := ScanDeep(context.Background(), profile, nil, "test-model")
	if err != nil {
		t.Fatalf("expected nil error for nil client, got %v", err)
	}
	// Pass should NOT be marked since we returned early
	if profile.PassCompleted(3) {
		t.Error("pass 3 should not be marked when client is nil")
	}
}

func TestScanDeep_WithReplayClient(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# My Project\nThis is a test project for unit testing.\n")
	writeFile(t, dir, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	profile := &RepoProfile{
		RepoPath: dir,
		TechStack: TechStackDetail{
			PrimaryLanguage:  "go",
			PrimaryBuildTool: "go",
		},
		Structure: RepoStructure{
			EntryPoints: []EntryPoint{{Path: "main.go", Kind: "main"}},
		},
		Test: TestConfig{
			TestFilePattern: "*_test.go",
		},
	}

	// Use replay client to return a canned response
	summary := "1. PROJECT PURPOSE: A hello world Go application.\n2. ARCHITECTURE: Single-file CLI.\n3. KEY PATTERNS: Direct main function.\n4. GOTCHAS: None."
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: summary,
		Model:   "test-model",
	})

	err := ScanDeep(context.Background(), profile, client, "test-model")
	if err != nil {
		t.Fatalf("ScanDeep failed: %v", err)
	}

	if !profile.PassCompleted(3) {
		t.Error("expected pass 3 to be marked completed")
	}

	// Check that LLM summary signal was added
	foundSummary := false
	for _, s := range profile.Signals {
		if s.Kind == "llm_summary" {
			foundSummary = true
			mustContain(t, s.Message, "PROJECT PURPOSE")
		}
	}
	if !foundSummary {
		t.Error("expected llm_summary signal")
	}
}

func TestScanDeep_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	// No files at all — gatherKeyFiles should return empty

	profile := &RepoProfile{
		RepoPath: dir,
		TechStack: TechStackDetail{
			PrimaryLanguage: "go",
		},
	}

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "should not be called",
		Model:   "test",
	})

	err := ScanDeep(context.Background(), profile, client, "test")
	if err != nil {
		t.Fatalf("ScanDeep failed: %v", err)
	}

	// Pass should be marked even for empty repos
	if !profile.PassCompleted(3) {
		t.Error("expected pass 3 to be marked even for empty repo")
	}

	// No LLM summary should exist since no files were gathered
	for _, s := range profile.Signals {
		if s.Kind == "llm_summary" {
			t.Error("should not have llm_summary for empty repo")
		}
	}
}

func TestGatherKeyFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Hello\nThis is a project.\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, dir, "foo_test.go", "package main\nfunc TestFoo(t *testing.T) {}\n")
	writeFile(t, dir, "CLAUDE.md", "Build: go build\n")

	profile := &RepoProfile{
		RepoPath: dir,
		Structure: RepoStructure{
			EntryPoints: []EntryPoint{{Path: "main.go", Kind: "main"}},
		},
		Test: TestConfig{
			TestFilePattern: "*_test.go",
		},
	}

	content := gatherKeyFiles(profile)
	mustContain(t, content, "README.md")
	mustContain(t, content, "main.go")
	mustContain(t, content, "foo_test.go")
	mustContain(t, content, "CLAUDE.md")
}

func TestReadFileTruncated(t *testing.T) {
	dir := t.TempDir()
	// Write a 100-byte file
	writeFile(t, dir, "big.txt", repeatStr("x", 100))

	content := readFileTruncated(dir+"/big.txt", 50)
	if len(content) != 50 {
		t.Errorf("expected 50 bytes, got %d", len(content))
	}

	// Non-existent file
	content = readFileTruncated(dir+"/nope.txt", 50)
	if content != "" {
		t.Errorf("expected empty string for missing file, got %q", content)
	}
}
