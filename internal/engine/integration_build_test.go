package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntegrationBuild_DetectsGoProject verifies that a directory containing
// go.mod is classified as projectGo.
func TestIntegrationBuild_DetectsGoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kind := detectProjectKind(dir)
	if kind != projectGo {
		t.Errorf("expected projectGo, got %v", kind)
	}
}

// TestIntegrationBuild_DetectsNodeProject verifies that package.json → projectNode.
func TestIntegrationBuild_DetectsNodeProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	kind := detectProjectKind(dir)
	if kind != projectNode {
		t.Errorf("expected projectNode, got %v", kind)
	}
}

// TestIntegrationBuild_DetectsRustProject verifies that Cargo.toml → projectRust.
func TestIntegrationBuild_DetectsRustProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kind := detectProjectKind(dir)
	if kind != projectRust {
		t.Errorf("expected projectRust, got %v", kind)
	}
}

// TestIntegrationBuild_UnknownProjectIsNoop verifies that an empty directory
// returns projectUnknown and runIntegrationBuild returns nil.
func TestIntegrationBuild_UnknownProjectIsNoop(t *testing.T) {
	dir := t.TempDir()

	kind := detectProjectKind(dir)
	if kind != projectUnknown {
		t.Errorf("expected projectUnknown, got %v", kind)
	}

	if err := runIntegrationBuild(dir); err != nil {
		t.Errorf("expected nil for unknown project, got %v", err)
	}
}

// TestIntegrationBuild_NodeWithoutBuildScript verifies that package.json
// without a "build" script causes runIntegrationBuild to return nil (skip).
func TestIntegrationBuild_NodeWithoutBuildScript(t *testing.T) {
	dir := t.TempDir()
	// No "build" script in scripts section.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test","scripts":{"test":"echo ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runIntegrationBuild(dir); err != nil {
		t.Errorf("expected nil (no build script), got %v", err)
	}
}

// TestIntegrationBuild_GoProjectFailsOnBrokenCode verifies that a Go project
// with a syntax error causes runIntegrationBuild to return a non-nil error
// containing the build output.
func TestIntegrationBuild_GoProjectFailsOnBrokenCode(t *testing.T) {
	dir := t.TempDir()

	// Minimal go.mod
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module broken.example.com\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Syntactically broken Go file.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { BROKEN SYNTAX }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runIntegrationBuild(dir)
	if err == nil {
		t.Fatal("expected build error for broken Go code, got nil")
	}
	// The error should contain something from the compiler.
	if len(err.Error()) == 0 {
		t.Error("expected non-empty error message from build failure")
	}
}

// TestBuildFileExists verifies the buildFileExists helper.
func TestBuildFileExists_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !buildFileExists(path) {
		t.Error("expected buildFileExists to return true for existing file")
	}
}

func TestBuildFileExists_NonExistentFile(t *testing.T) {
	if buildFileExists("/nonexistent/path/go.mod") {
		t.Error("expected buildFileExists to return false for nonexistent file")
	}
}
