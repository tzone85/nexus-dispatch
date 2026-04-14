package engine

import (
	"os"
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

	// Pre-create .gitignore with all patterns already present
	existing := "CLAUDE.md\n.nxd-prompts/\n.serena/\n"
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
