package repolearn

import (
	"os/exec"
	"testing"
)

// --------------------------------------------------------------------------
// Commit format detection (pure function tests on regex)
// --------------------------------------------------------------------------

func TestConventionalRe(t *testing.T) {
	tests := []struct {
		msg   string
		match bool
	}{
		{"feat: add user login", true},
		{"fix(auth): handle nil token", true},
		{"docs: update README", true},
		{"chore: bump dependencies", true},
		{"refactor(engine): simplify loop", true},
		{"ci: add GitHub Actions", true},
		{"revert: undo bad commit", true},
		{"Add login feature", false},
		{"JIRA-123 fix something", false},
		{"", false},
	}
	for _, tt := range tests {
		got := conventionalRe.MatchString(tt.msg)
		if got != tt.match {
			t.Errorf("conventionalRe.Match(%q) = %v, want %v", tt.msg, got, tt.match)
		}
	}
}

func TestTicketPrefixRe(t *testing.T) {
	tests := []struct {
		msg   string
		match bool
	}{
		{"JIRA-123 fix login", true},
		{"PROJ-1 initial commit", true},
		{"AB-99 something", true},
		{"feat: not a ticket", false},
		{"lowercase-123 nope", false},
		{"123 just a number", false},
	}
	for _, tt := range tests {
		got := ticketPrefixRe.MatchString(tt.msg)
		if got != tt.match {
			t.Errorf("ticketPrefixRe.Match(%q) = %v, want %v", tt.msg, got, tt.match)
		}
	}
}

// --------------------------------------------------------------------------
// Full ScanHistory with a temp git repo
// --------------------------------------------------------------------------

func TestScanHistory_ConventionalCommits(t *testing.T) {
	dir := initGitRepo(t)

	// Create conventional commits
	for _, msg := range []string{
		"feat: add initial scaffold",
		"feat: add user model",
		"fix: handle nil pointer in auth",
		"docs: update getting started guide",
		"test: add unit tests for user model",
		"chore: update CI config",
	} {
		writeFile(t, dir, "dummy.txt", msg) // change content each time
		gitAdd(t, dir)
		gitCommit(t, dir, msg)
	}

	profile := &RepoProfile{RepoPath: dir}
	if err := ScanHistory(profile); err != nil {
		t.Fatalf("ScanHistory failed: %v", err)
	}

	assertEqual(t, "CommitFormat", "conventional", profile.Conventions.CommitFormat)
	if profile.Conventions.CommitCount < 6 {
		t.Errorf("expected at least 6 commits, got %d", profile.Conventions.CommitCount)
	}
	if profile.Conventions.ContributorCount < 1 {
		t.Errorf("expected at least 1 contributor, got %d", profile.Conventions.ContributorCount)
	}
	if profile.Conventions.ActiveDays < 1 {
		t.Errorf("expected at least 1 active day, got %d", profile.Conventions.ActiveDays)
	}
	if !profile.PassCompleted(2) {
		t.Error("expected pass 2 to be marked completed")
	}

	// Should detect solo project
	foundSolo := false
	for _, s := range profile.Signals {
		if s.Kind == "solo_project" {
			foundSolo = true
		}
	}
	if !foundSolo {
		t.Error("expected solo_project signal for single-contributor repo")
	}
}

func TestScanHistory_ChurnHotspots(t *testing.T) {
	dir := initGitRepo(t)

	// Create a file and modify it multiple times
	for i := 0; i < 5; i++ {
		writeFile(t, dir, "hot.go", repeatStr("line\n", i+1))
		gitAdd(t, dir)
		gitCommit(t, dir, "feat: modify hot.go")
	}
	// Create a cold file
	writeFile(t, dir, "cold.go", "package main\n")
	gitAdd(t, dir)
	gitCommit(t, dir, "feat: add cold.go")

	profile := &RepoProfile{RepoPath: dir}
	if err := ScanHistory(profile); err != nil {
		t.Fatalf("ScanHistory failed: %v", err)
	}

	if len(profile.Conventions.ChurnHotspots) == 0 {
		t.Fatal("expected churn hotspots")
	}
	// hot.go should be the top hotspot
	if profile.Conventions.ChurnHotspots[0].Path != "hot.go" {
		t.Errorf("expected hot.go as top hotspot, got %q", profile.Conventions.ChurnHotspots[0].Path)
	}
	if profile.Conventions.ChurnHotspots[0].Changes < 5 {
		t.Errorf("expected hot.go to have at least 5 changes, got %d", profile.Conventions.ChurnHotspots[0].Changes)
	}
}

func TestScanHistory_NotAGitRepo(t *testing.T) {
	dir := t.TempDir() // no git init
	profile := &RepoProfile{RepoPath: dir}
	err := ScanHistory(profile)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	// Initial commit so HEAD exists
	writeFile(t, dir, ".gitkeep", "")
	gitAdd(t, dir)
	gitCommit(t, dir, "chore: initial commit")
	return dir
}

func gitAdd(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "add", "-A")
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "git", "commit", "-m", msg, "--allow-empty-message")
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %v: %v\n%s", name, args, err, out)
	}
}

func repeatStr(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
