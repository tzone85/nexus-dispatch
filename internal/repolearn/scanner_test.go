package repolearn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --------------------------------------------------------------------------
// Full ScanStatic integration tests
// --------------------------------------------------------------------------

func TestScanStatic_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Scaffold a Go project
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26.1\n\nrequire (\n\tgithub.com/spf13/cobra v1.10.2 // indirect\n)\n")
	writeFile(t, dir, "go.sum", "")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, dir, "Makefile", ".PHONY: build test lint\n\nbuild:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n\nlint:\n\tgolangci-lint run\n")
	writeFile(t, dir, ".golangci.yml", "run:\n  timeout: 5m\n")
	mkDir(t, dir, "cmd/app")
	writeFile(t, dir, "cmd/app/main.go", "package main\nfunc main() {}\n")
	mkDir(t, dir, "internal/engine")
	writeFile(t, dir, "internal/engine/planner.go", "package engine\n")
	writeFile(t, dir, "internal/engine/planner_test.go", "package engine\n")
	mkDir(t, dir, ".github/workflows")
	writeFile(t, dir, ".github/workflows/ci.yml", "name: CI\n")

	profile, err := ScanStatic(dir)
	if err != nil {
		t.Fatalf("ScanStatic failed: %v", err)
	}

	// Tech stack
	assertEqual(t, "PrimaryLanguage", "go", profile.TechStack.PrimaryLanguage)
	assertEqual(t, "PrimaryBuildTool", "go", profile.TechStack.PrimaryBuildTool)
	assertEqual(t, "LanguageVersion", "1.26.1", profile.TechStack.LanguageVersion)

	// Build config (Makefile targets take priority)
	assertEqual(t, "BuildCommand", "make build", profile.Build.BuildCommand)
	assertEqual(t, "LintCommand", "make lint", profile.Build.LintCommand)

	// Test config
	assertEqual(t, "TestCommand", "make test", profile.Test.TestCommand)
	assertEqual(t, "TestFramework", "go test", profile.Test.TestFramework)
	assertEqual(t, "TestFilePattern", "*_test.go", profile.Test.TestFilePattern)

	// CI
	assertEqual(t, "CI.System", "github_actions", profile.CI.System)
	if len(profile.CI.Files) == 0 {
		t.Error("expected CI files to be populated")
	}

	// Structure
	if profile.Structure.TotalFiles == 0 {
		t.Error("expected TotalFiles > 0")
	}
	if profile.Structure.SourceFiles == 0 {
		t.Error("expected SourceFiles > 0")
	}

	// Entry points
	foundCmd := false
	foundMain := false
	for _, ep := range profile.Structure.EntryPoints {
		if ep.Path == "cmd/app/main.go" && ep.Kind == "cmd" {
			foundCmd = true
		}
		if ep.Path == "main.go" && ep.Kind == "main" {
			foundMain = true
		}
	}
	if !foundCmd {
		t.Error("expected cmd/app/main.go entry point")
	}
	if !foundMain {
		t.Error("expected main.go entry point")
	}

	// Dependencies
	if len(profile.Dependencies) == 0 {
		t.Error("expected dependencies from go.mod")
	}
	foundCobra := false
	for _, dep := range profile.Dependencies {
		if dep.Name == "github.com/spf13/cobra" {
			foundCobra = true
			assertEqual(t, "cobra kind", "indirect", dep.Kind)
		}
	}
	if !foundCobra {
		t.Error("expected cobra dependency from go.mod")
	}

	// Pass completed
	if !profile.PassCompleted(1) {
		t.Error("expected pass 1 to be marked completed")
	}
	if profile.Iteration != 1 {
		t.Errorf("expected iteration 1, got %d", profile.Iteration)
	}
}

func TestScanStatic_PythonProject(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "requirements.txt", "flask==2.3.0\nrequests>=2.28.0\n")
	writeFile(t, dir, "pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n\n[project]\nrequires-python = \">=3.10\"\n")
	writeFile(t, dir, "app.py", "from flask import Flask\napp = Flask(__name__)\n")
	writeFile(t, dir, "ruff.toml", "line-length = 88\n")
	mkDir(t, dir, "tests")
	writeFile(t, dir, "tests/test_app.py", "def test_hello():\n    pass\n")

	profile, err := ScanStatic(dir)
	if err != nil {
		t.Fatalf("ScanStatic failed: %v", err)
	}

	assertEqual(t, "PrimaryLanguage", "python", profile.TechStack.PrimaryLanguage)
	assertEqual(t, "PrimaryFramework", "Flask", profile.TechStack.PrimaryFramework)
	assertEqual(t, "LanguageVersion", ">=3.10", profile.TechStack.LanguageVersion)
	assertEqual(t, "LintCommand", "ruff check .", profile.Build.LintCommand)
	assertEqual(t, "TestFramework", "pytest", profile.Test.TestFramework)
	assertEqual(t, "TestCommand", "pytest", profile.Test.TestCommand)

	// Dependencies
	foundFlask := false
	for _, dep := range profile.Dependencies {
		if dep.Name == "flask" {
			foundFlask = true
			assertEqual(t, "flask version", "2.3.0", dep.Version)
		}
	}
	if !foundFlask {
		t.Error("expected flask dependency")
	}
}

func TestScanStatic_TypeScriptProject(t *testing.T) {
	dir := t.TempDir()

	pkgJSON := map[string]any{
		"name": "my-app",
		"main": "dist/index.js",
		"scripts": map[string]any{
			"build":  "tsc",
			"lint":   "eslint .",
			"test":   "vitest",
			"format": "prettier --write .",
		},
		"dependencies": map[string]any{
			"react":     "^18.2.0",
			"next":      "^14.0.0",
			"express":   "^4.18.0",
		},
		"devDependencies": map[string]any{
			"typescript": "^5.0.0",
			"vitest":     "^1.0.0",
			"eslint":     "^8.0.0",
		},
	}
	data, _ := json.Marshal(pkgJSON)
	writeFile(t, dir, "package.json", string(data))
	writeFile(t, dir, "tsconfig.json", "{}\n")
	writeFile(t, dir, "yarn.lock", "")
	writeFile(t, dir, "src/index.ts", "export const hello = 'world';\n")
	writeFile(t, dir, "src/app.tsx", "export default function App() { return <div/>; }\n")

	profile, err := ScanStatic(dir)
	if err != nil {
		t.Fatalf("ScanStatic failed: %v", err)
	}

	assertEqual(t, "PrimaryLanguage", "typescript", profile.TechStack.PrimaryLanguage)
	assertEqual(t, "PrimaryBuildTool", "yarn", profile.TechStack.PrimaryBuildTool)
	assertEqual(t, "PrimaryFramework", "Next.js", profile.TechStack.PrimaryFramework)
	assertEqual(t, "BuildCommand", "yarn build", profile.Build.BuildCommand)
	assertEqual(t, "LintCommand", "yarn lint", profile.Build.LintCommand)
	assertEqual(t, "FormatCommand", "yarn format", profile.Build.FormatCommand)
	assertEqual(t, "TestCommand", "yarn test", profile.Test.TestCommand)
	assertEqual(t, "TestFramework", "vitest", profile.Test.TestFramework)
	assertEqual(t, "TestFilePattern", "*.test.ts", profile.Test.TestFilePattern)

	// Dependencies
	if len(profile.Dependencies) == 0 {
		t.Error("expected dependencies from package.json")
	}
	foundReact := false
	foundVitest := false
	for _, dep := range profile.Dependencies {
		if dep.Name == "react" {
			foundReact = true
			assertEqual(t, "react kind", "direct", dep.Kind)
		}
		if dep.Name == "vitest" {
			foundVitest = true
			assertEqual(t, "vitest kind", "dev", dep.Kind)
		}
	}
	if !foundReact {
		t.Error("expected react dependency")
	}
	if !foundVitest {
		t.Error("expected vitest dependency")
	}
}

// --------------------------------------------------------------------------
// Makefile parsing
// --------------------------------------------------------------------------

func TestParseMakefileTargets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", `.PHONY: build test lint clean install

BINARY=vxd

build:
	go build -o $(BINARY) ./cmd/vxd/

test:
	go test ./... -race

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

install: build
	cp $(BINARY) /usr/local/bin/
`)

	targets := parseMakefileTargets(dir)
	expected := []string{"build", "test", "lint", "clean", "install"}
	if len(targets) != len(expected) {
		t.Errorf("expected %d targets, got %d: %v", len(expected), len(targets), targets)
		return
	}
	for i, exp := range expected {
		if targets[i] != exp {
			t.Errorf("target[%d]: expected %q, got %q", i, exp, targets[i])
		}
	}
}

func TestParseMakefileTargets_NoMakefile(t *testing.T) {
	dir := t.TempDir()
	targets := parseMakefileTargets(dir)
	if targets != nil {
		t.Errorf("expected nil targets for missing Makefile, got %v", targets)
	}
}

// --------------------------------------------------------------------------
// CI detection
// --------------------------------------------------------------------------

func TestDetectCI_GitHubActions(t *testing.T) {
	dir := t.TempDir()
	mkDir(t, dir, ".github/workflows")
	writeFile(t, dir, ".github/workflows/ci.yml", "name: CI\n")
	writeFile(t, dir, ".github/workflows/release.yaml", "name: Release\n")

	ci := detectCI(dir)
	assertEqual(t, "CI.System", "github_actions", ci.System)
	if len(ci.Files) != 2 {
		t.Errorf("expected 2 CI files, got %d: %v", len(ci.Files), ci.Files)
	}
}

func TestDetectCI_GitLabCI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitlab-ci.yml", "stages:\n  - test\n")

	ci := detectCI(dir)
	assertEqual(t, "CI.System", "gitlab_ci", ci.System)
}

func TestDetectCI_None(t *testing.T) {
	dir := t.TempDir()
	ci := detectCI(dir)
	if ci.System != "" {
		t.Errorf("expected empty CI system, got %q", ci.System)
	}
}

// --------------------------------------------------------------------------
// Go version extraction
// --------------------------------------------------------------------------

func TestExtractGoVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module foo\n\ngo 1.26.1\n")
	version := extractGoVersion(dir)
	assertEqual(t, "go version", "1.26.1", version)
}

func TestExtractGoVersion_NoMod(t *testing.T) {
	dir := t.TempDir()
	version := extractGoVersion(dir)
	if version != "" {
		t.Errorf("expected empty version, got %q", version)
	}
}

// --------------------------------------------------------------------------
// Directory classification
// --------------------------------------------------------------------------

func TestClassifyDir(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"cmd", "commands"},
		{"internal", "source"},
		{"pkg", "source"},
		{"src", "source"},
		{"test", "test"},
		{"tests", "test"},
		{"docs", "docs"},
		{"vendor", "vendor"},
		{"scripts", "scripts"},
		{"config", "config"},
		{"migrations", "database"},
		{"api", "api"},
		{"web", "web"},
		{"deploy", "infrastructure"},
		{"terraform", "infrastructure"},
		{"examples", "examples"},
		{"build", "build"},
		{"generated", "generated"},
		{"foobar", "source"}, // default
	}

	for _, tt := range tests {
		got := classifyDir(tt.name)
		if got != tt.expected {
			t.Errorf("classifyDir(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

// --------------------------------------------------------------------------
// Signal detection
// --------------------------------------------------------------------------

func TestDetectSignals_Docker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM golang:1.26\n")
	writeFile(t, dir, "docker-compose.yml", "version: '3'\n")
	writeFile(t, dir, "go.mod", "module foo\ngo 1.26\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	profile, _ := ScanStatic(dir)

	foundDocker := false
	foundCompose := false
	for _, s := range profile.Signals {
		if s.Kind == "docker" {
			foundDocker = true
		}
		if s.Kind == "docker_compose" {
			foundCompose = true
		}
	}
	if !foundDocker {
		t.Error("expected docker signal")
	}
	if !foundCompose {
		t.Error("expected docker_compose signal")
	}
}

func TestDetectSignals_NoTests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module foo\ngo 1.26\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	// No test files at all

	profile, _ := ScanStatic(dir)

	foundNoTests := false
	for _, s := range profile.Signals {
		if s.Kind == "no_tests" {
			foundNoTests = true
		}
	}
	if !foundNoTests {
		t.Error("expected no_tests signal when repo has no test files")
	}
}

// --------------------------------------------------------------------------
// Profile persistence
// --------------------------------------------------------------------------

func TestSaveAndLoadProfile(t *testing.T) {
	dir := t.TempDir()

	original := &RepoProfile{
		RepoPath: "/some/path",
		TechStack: TechStackDetail{
			PrimaryLanguage:  "go",
			PrimaryBuildTool: "go",
			LanguageVersion:  "1.26.1",
		},
		Build: BuildConfig{
			BuildCommand: "go build ./...",
			LintCommand:  "go vet ./...",
		},
		Test: TestConfig{
			TestCommand:   "go test ./...",
			TestFramework: "go test",
		},
		Signals: []Signal{
			{Kind: "docker", Message: "Dockerfile present", Path: "Dockerfile"},
		},
	}
	original.MarkPass(1)

	if err := SaveProfile(dir, original); err != nil {
		t.Fatalf("SaveProfile failed: %v", err)
	}

	loaded, err := LoadProfile(dir)
	if err != nil {
		t.Fatalf("LoadProfile failed: %v", err)
	}

	assertEqual(t, "RepoPath", original.RepoPath, loaded.RepoPath)
	assertEqual(t, "PrimaryLanguage", original.TechStack.PrimaryLanguage, loaded.TechStack.PrimaryLanguage)
	assertEqual(t, "BuildCommand", original.Build.BuildCommand, loaded.Build.BuildCommand)
	assertEqual(t, "TestCommand", original.Test.TestCommand, loaded.Test.TestCommand)
	if !loaded.PassCompleted(1) {
		t.Error("expected pass 1 to be marked completed after load")
	}
}

func TestLoadProfile_NotExist(t *testing.T) {
	dir := t.TempDir()
	profile, err := LoadProfile(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing profile, got %v", err)
	}
	if profile.TechStack.PrimaryLanguage != "" {
		t.Error("expected empty profile for missing file")
	}
}

// --------------------------------------------------------------------------
// Profile summary
// --------------------------------------------------------------------------

func TestProfileSummary(t *testing.T) {
	profile := &RepoProfile{
		TechStack: TechStackDetail{
			PrimaryLanguage:  "go",
			PrimaryBuildTool: "go",
			LanguageVersion:  "1.26.1",
		},
		Build: BuildConfig{
			BuildCommand: "make build",
			LintCommand:  "make lint",
		},
		Test: TestConfig{
			TestCommand:   "make test",
			TestFramework: "go test",
		},
		CI: CIConfig{System: "github_actions"},
		Structure: RepoStructure{
			TotalFiles:  100,
			SourceFiles: 50,
			EntryPoints: []EntryPoint{{Path: "cmd/vxd/main.go", Kind: "cmd"}},
		},
		Conventions: Conventions{
			ContributorCount: 3,
			CommitCount:      150,
			CommitFormat:     "conventional",
		},
	}

	summary := profile.Summary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	mustContain(t, summary, "go")
	mustContain(t, summary, "1.26.1")
	mustContain(t, summary, "make build")
	mustContain(t, summary, "make lint")
	mustContain(t, summary, "make test")
	mustContain(t, summary, "github_actions")
	mustContain(t, summary, "cmd/vxd/main.go")
	mustContain(t, summary, "Contributors: 3")
	mustContain(t, summary, "conventional")
}

func TestProfileSummary_Empty(t *testing.T) {
	profile := &RepoProfile{}
	if summary := profile.Summary(); summary != "" {
		t.Errorf("expected empty summary for empty profile, got %q", summary)
	}
}

// --------------------------------------------------------------------------
// AddSignal deduplication
// --------------------------------------------------------------------------

func TestAddSignal_Dedup(t *testing.T) {
	p := &RepoProfile{}
	p.AddSignal("docker", "Dockerfile present", "Dockerfile")
	p.AddSignal("docker", "Dockerfile present", "Dockerfile") // duplicate
	p.AddSignal("docker", "Another docker file", "other/Dockerfile")

	if len(p.Signals) != 2 {
		t.Errorf("expected 2 signals (deduped), got %d", len(p.Signals))
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func writeFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func mkDir(t *testing.T, base, relPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, relPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
}

func assertEqual(t *testing.T, field, expected, actual string) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %q, got %q", field, expected, actual)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !containsStr(haystack, needle) {
		t.Errorf("expected summary to contain %q, but it didn't.\nSummary:\n%s", needle, haystack)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
