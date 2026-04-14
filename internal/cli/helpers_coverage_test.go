package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/repolearn"
)

func TestExpandHome_ExtendedCases(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~/Documents", filepath.Join(home, "Documents")},
		{"~", home},
	}
	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatPasses(t *testing.T) {
	tests := []struct {
		input []int
		want  string
	}{
		{nil, "none"},
		{[]int{}, "none"},
		{[]int{1}, "1"},
		{[]int{1, 2}, "1, 2"},
		{[]int{1, 2, 3}, "1, 2, 3"},
	}
	for _, tt := range tests {
		got := formatPasses(tt.input)
		if got != tt.want {
			t.Errorf("formatPasses(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMergeStaticIntoProfile(t *testing.T) {
	existing := &repolearn.RepoProfile{
		Conventions: repolearn.Conventions{
			CommitFormat:     "conventional",
			ContributorCount: 5,
		},
		CompletedPasses: []int{2}, // has pass 2 from git history
	}

	scanned := &repolearn.RepoProfile{
		TechStack: repolearn.TechStackDetail{
			PrimaryLanguage:  "go",
			PrimaryBuildTool: "go",
		},
		Build: repolearn.BuildConfig{
			BuildCommand: "go build ./...",
		},
		Test: repolearn.TestConfig{
			TestCommand: "go test ./...",
		},
	}
	scanned.AddSignal("docker", "Dockerfile present", "Dockerfile")

	mergeStaticIntoProfile(existing, scanned)

	// Tech stack should be overwritten from scan
	if existing.TechStack.PrimaryLanguage != "go" {
		t.Errorf("expected go, got %q", existing.TechStack.PrimaryLanguage)
	}
	if existing.Build.BuildCommand != "go build ./..." {
		t.Errorf("expected build command, got %q", existing.Build.BuildCommand)
	}

	// Conventions (pass 2) should be preserved
	if existing.Conventions.ContributorCount != 5 {
		t.Error("conventions should be preserved from existing profile")
	}

	// Pass 1 should now be completed
	if !existing.PassCompleted(1) {
		t.Error("pass 1 should be marked completed after merge")
	}

	// Signal should be added
	found := false
	for _, s := range existing.Signals {
		if s.Kind == "docker" {
			found = true
		}
	}
	if !found {
		t.Error("docker signal should be merged")
	}
}

func TestLoadConfig_WithValidFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(configPath, []byte(`workspace:
  state_dir: /tmp/nxd-test
  backend: sqlite
`), 0o644)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Workspace.StateDir != "/tmp/nxd-test" {
		t.Errorf("state_dir = %q, want /tmp/nxd-test", cfg.Workspace.StateDir)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(configPath, []byte("{{invalid yaml"), 0o644)

	_, err := loadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

