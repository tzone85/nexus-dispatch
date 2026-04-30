package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHallucinationLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"Looking at the code, I think...", true},
		{"I'll add a new function here.", true},
		{"Here's the implementation:", true},
		{"Based on the requirements", true},
		{"package main", false},
		{"func Foo() {", false},
		{"// this is a comment", false},
		{"", false},
		{"   ", false},
	} {
		if got := isHallucinationLine(tc.in); got != tc.want {
			t.Errorf("isHallucinationLine(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestScrubFile_RemovesPreamble(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	content := "Looking at this, I'll add a helper.\nHere's the code:\n\npackage main\n\nfunc Foo() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, removed, err := scrubFile(path)
	if err != nil {
		t.Fatalf("scrubFile: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "package main") {
		t.Errorf("post-scrub does not start with package decl: %q", string(got))
	}
}

func TestScrubFile_NoHallucination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	content := "package main\n\nfunc Foo() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _, err := scrubFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("clean file should not be scrubbed")
	}
}

func TestScrubFile_EntirelyHallucination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	content := "Looking at the requirements\nI'll plan it out.\nHere's my approach.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _, err := scrubFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("entirely-hallucination file should be left alone for inspection")
	}
	// content should be unchanged.
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("content mutated: %q", string(got))
	}
}

func TestScanFileForConflictMarkers(t *testing.T) {
	dir := t.TempDir()
	withMarkers := filepath.Join(dir, "a.go")
	clean := filepath.Join(dir, "b.go")
	os.WriteFile(withMarkers, []byte("package main\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> branch\n"), 0o644)
	os.WriteFile(clean, []byte("package main\nfunc Foo() {}\n"), 0o644)

	gotBad, err := scanFileForConflictMarkers(withMarkers)
	if err != nil {
		t.Fatal(err)
	}
	if !gotBad {
		t.Error("expected conflict markers to be detected")
	}
	gotClean, err := scanFileForConflictMarkers(clean)
	if err != nil {
		t.Fatal(err)
	}
	if gotClean {
		t.Error("clean file flagged as conflicted")
	}
}

func TestValidateBuild_GoOK(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testpkg\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package testpkg\n"), 0o644)
	if err := validateBuild(context.Background(), dir); err != nil {
		t.Errorf("validateBuild: %v", err)
	}
}

func TestValidateBuild_GoBroken(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testpkg\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package testpkg\n\nfunc broken {\n"), 0o644)
	if err := validateBuild(context.Background(), dir); err == nil {
		t.Error("expected build failure")
	}
}

func TestValidateBuild_NoMarker(t *testing.T) {
	dir := t.TempDir()
	if err := validateBuild(context.Background(), dir); err != nil {
		t.Errorf("expected nil for unknown project: %v", err)
	}
}

func TestCaptureFileTree_NotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	if got := captureFileTree(dir); got != "" {
		t.Errorf("expected empty for non-repo, got %q", got)
	}
}
