package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGitignorePatterns_CreatesNew(t *testing.T) {
	dir := t.TempDir()

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	for _, pat := range []string{"CLAUDE.md", ".nxd-prompts/", ".serena/"} {
		if !strings.Contains(string(content), pat) {
			t.Errorf("expected .gitignore to contain %q", pat)
		}
	}
}

func TestEnsureGitignorePatterns_ExistingPatterns(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// Pre-create .gitignore with all patterns already present (including new ones)
	existing := "CLAUDE.md\n.nxd-prompts/\n.serena/\nWAVE_CONTEXT.md\nREQUIREMENT.md\nnxd.yaml\n.nxd-fix-gaps.md\n"
	os.WriteFile(giPath, []byte(existing), 0o644)

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	// Should not add duplicates
	if string(content) != existing {
		t.Errorf("expected no changes, but .gitignore was modified to:\n%s", string(content))
	}
}

func TestEnsureGitignorePatterns_PartialExisting(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// Only has CLAUDE.md
	os.WriteFile(giPath, []byte("CLAUDE.md\nnode_modules/\n"), 0o644)

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	s := string(content)
	// Should keep existing content and add missing patterns
	if !strings.Contains(s, "node_modules/") {
		t.Error("expected existing node_modules/ to be preserved")
	}
	if !strings.Contains(s, ".nxd-prompts/") {
		t.Error("expected .nxd-prompts/ to be added")
	}
	if !strings.Contains(s, ".serena/") {
		t.Error("expected .serena/ to be added")
	}

	// Count occurrences of CLAUDE.md -- should only appear once
	count := strings.Count(s, "CLAUDE.md")
	if count != 1 {
		t.Errorf("expected CLAUDE.md to appear once, appeared %d times", count)
	}
}

func TestEnsureGitignorePatterns_NXDArtifactHeader(t *testing.T) {
	dir := t.TempDir()

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	if !strings.Contains(string(content), "# NXD agent artifacts") {
		t.Error("expected NXD artifact header comment in .gitignore")
	}
}

func TestEnsureGitignorePatterns_NewPatterns(t *testing.T) {
	dir := t.TempDir()

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	// Check that all new patterns are included
	newPatterns := []string{"WAVE_CONTEXT.md", "REQUIREMENT.md", "nxd.yaml", ".nxd-fix-gaps.md"}
	for _, pat := range newPatterns {
		if !strings.Contains(string(content), pat) {
			t.Errorf("expected .gitignore to contain new pattern %q", pat)
		}
	}

	// Check that original patterns are still there
	originalPatterns := []string{"CLAUDE.md", ".nxd-prompts/", ".serena/"}
	for _, pat := range originalPatterns {
		if !strings.Contains(string(content), pat) {
			t.Errorf("expected .gitignore to contain original pattern %q", pat)
		}
	}
}

func TestCaptureFileTree(t *testing.T) {
	dir := t.TempDir()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(dir)

	// Initialize a git repository in the current directory
	if err := exec.Command("git", "init").Run(); err != nil {
		// Skip test if git is not available
		t.Skip("git not available")
	}
	if err := exec.Command("git", "config", "user.email", "test@example.com").Run(); err != nil {
		t.Skip("git config failed")
	}
	if err := exec.Command("git", "config", "user.name", "Test User").Run(); err != nil {
		t.Skip("git config failed")
	}

	// Create some test files
	files := []string{"main.go", "helper.go", "README.md"}
	for _, file := range files {
		err := os.WriteFile(file, []byte("test content"), 0o644)
		if err != nil {
			t.Fatalf("create file %s: %v", file, err)
		}
	}

	// Add files to git
	if err := exec.Command("git", "add", ".").Run(); err != nil {
		t.Fatalf("git add .: %v", err)
	}

	// Test captureFileTree
	result := captureFileTree(dir)

	// Verify all files are in the result
	for _, file := range files {
		if !strings.Contains(result, file) {
			t.Errorf("expected file %s to be in file tree output: %s", file, result)
		}
	}

	// Test with non-git directory
	nonGitDir := t.TempDir()
	result = captureFileTree(nonGitDir)
	if result != "" {
		t.Errorf("expected empty result for non-git directory, got: %s", result)
	}
}
