package repolearn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfilePath(t *testing.T) {
	got := ProfilePath("/home/user/.nxd/projects/foo")
	want := "/home/user/.nxd/projects/foo/repo-profile.json"
	if got != want {
		t.Errorf("ProfilePath = %q, want %q", got, want)
	}
}

func TestExtractCargoVersion(t *testing.T) {
	dir := t.TempDir()

	// No Cargo.toml
	if v := extractCargoVersion(dir); v != "" {
		t.Errorf("expected empty for missing Cargo.toml, got %q", v)
	}

	// With rust-version
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`[package]
name = "myapp"
rust-version = "1.75.0"
`), 0o644)
	if v := extractCargoVersion(dir); v != "1.75.0" {
		t.Errorf("expected 1.75.0, got %q", v)
	}

	// With edition only
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`[package]
name = "myapp"
edition = "2021"
`), 0o644)
	if v := extractCargoVersion(dir); v != "edition 2021" {
		t.Errorf("expected 'edition 2021', got %q", v)
	}
}

func TestExtractPythonVersion(t *testing.T) {
	dir := t.TempDir()

	// No pyproject.toml
	if v := extractPythonVersion(dir); v != "" {
		t.Errorf("expected empty for missing pyproject.toml, got %q", v)
	}

	// With requires-python
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
requires-python = ">=3.10"
`), 0o644)
	if v := extractPythonVersion(dir); v != ">=3.10" {
		t.Errorf("expected >=3.10, got %q", v)
	}
}

func TestDetectRubyFramework(t *testing.T) {
	dir := t.TempDir()

	// No Gemfile
	if f := detectRubyFramework(dir); f != "" {
		t.Errorf("expected empty for missing Gemfile, got %q", f)
	}

	// Rails app
	os.WriteFile(filepath.Join(dir, "Gemfile"), []byte(`source "https://rubygems.org"
gem "rails", "~> 7.1"
`), 0o644)
	if f := detectRubyFramework(dir); f != "Rails" {
		t.Errorf("expected Rails, got %q", f)
	}

	// Sinatra app
	os.WriteFile(filepath.Join(dir, "Gemfile"), []byte(`source "https://rubygems.org"
gem "sinatra"
`), 0o644)
	if f := detectRubyFramework(dir); f != "Sinatra" {
		t.Errorf("expected Sinatra, got %q", f)
	}
}

func TestParseRustDependencies(t *testing.T) {
	dir := t.TempDir()

	// No Cargo.toml
	deps := parseRustDependencies(dir)
	if len(deps) != 0 {
		t.Errorf("expected 0 deps for missing Cargo.toml, got %d", len(deps))
	}

	// With dependencies
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`[package]
name = "myapp"

[dependencies]
serde = "1.0"
tokio = { version = "1.0", features = ["full"] }

[dev-dependencies]
criterion = "0.5"
`), 0o644)
	deps = parseRustDependencies(dir)
	if len(deps) < 2 {
		t.Errorf("expected at least 2 deps, got %d", len(deps))
	}
}

func TestDetectCodeGraphSignals(t *testing.T) {
	// No graph DB
	profile := &RepoProfile{}
	detectCodeGraphSignals(profile, t.TempDir())
	for _, s := range profile.Signals {
		if s.Kind == "codegraph_stats" {
			t.Error("expected no codegraph signal for missing DB")
		}
	}
}

func TestCoverage_ScanStatic_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	profile, err := ScanStatic(dir)
	if err != nil {
		t.Fatalf("ScanStatic on empty dir: %v", err)
	}
	if profile.TechStack.PrimaryLanguage != "" {
		t.Errorf("expected empty primary language, got %q", profile.TechStack.PrimaryLanguage)
	}
	if !profile.PassCompleted(1) {
		t.Error("expected Pass 1 completed")
	}
}

func TestCoverage_ScanStatic_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Create a minimal Go project structure
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\ntest:\n\tgo test ./...\n"), 0o644)

	profile, err := ScanStatic(dir)
	if err != nil {
		t.Fatalf("ScanStatic: %v", err)
	}
	if profile.TechStack.PrimaryLanguage != "go" {
		t.Errorf("expected go, got %q", profile.TechStack.PrimaryLanguage)
	}
	if profile.TechStack.PrimaryBuildTool != "go" {
		t.Errorf("expected build tool 'go', got %q", profile.TechStack.PrimaryBuildTool)
	}
	if profile.Structure.TotalFiles < 3 {
		t.Errorf("expected at least 3 files, got %d", profile.Structure.TotalFiles)
	}
}
