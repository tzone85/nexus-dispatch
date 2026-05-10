package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewStore_MkdirFailure proves NewStore wraps the os.MkdirAll error
// rather than swallowing it. Triggered by pointing baseDir at a path
// component that already exists as a regular file (cannot be a parent).
func TestNewStore_MkdirFailure(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "block")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Try to create artifacts under <blocker>/artifacts — Mkdir fails
	// because <blocker> is a regular file, not a directory.
	_, err := NewStore(filepath.Join(blocker, "artifacts"))
	if err == nil {
		t.Fatal("expected error when baseDir parent is a file")
	}
	if !strings.Contains(err.Error(), "create artifact base dir") {
		t.Errorf("error should be wrapped, got %v", err)
	}
}

// TestStore_RejectsInvalidStoryID guards the sanitize.ValidIdentifier
// gate on every Store method that takes a storyID. A traversal-style
// id (..) or one with shell metacharacters must be refused with a
// clear "invalid story id" error.
func TestStore_RejectsInvalidStoryID(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	bad := "../escape"
	cases := []struct {
		name string
		op   func() error
	}{
		{"Init", func() error { return s.Init(bad) }},
		{"Write", func() error { return s.Write(bad, TypeReviewResult, map[string]any{}) }},
		{"WriteRaw", func() error { return s.WriteRaw(bad, TypeGitDiff, "diff") }},
		{"Append", func() error { return s.Append(bad, TypeTraceEvents, map[string]any{}) }},
		{"Read", func() error { _, err := s.Read(bad, "x.json"); return err }},
		{"List", func() error { _, err := s.List(bad); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.op()
			if err == nil {
				t.Fatal("expected rejection for traversal story id")
			}
			if !strings.Contains(err.Error(), "invalid story id") {
				t.Errorf("error should mention 'invalid story id', got %v", err)
			}
		})
	}
}

// TestStore_InitCreatesPerStoryDir covers the explicit Init path
// (currently 0% coverage). Some callers prefer to materialize the
// directory before issuing the first Write so trace files appear in
// the dashboard's file listing.
func TestStore_InitCreatesPerStoryDir(t *testing.T) {
	base := t.TempDir()
	s, err := NewStore(base)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Init("STORY-1"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	info, err := os.Stat(filepath.Join(base, "STORY-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Init must create a directory, got mode %s", info.Mode())
	}
	// Idempotent: calling Init again should not fail.
	if err := s.Init("STORY-1"); err != nil {
		t.Errorf("Init must be idempotent, got: %v", err)
	}
}

// TestStore_ReadRejectsTraversalFilename covers the second-half path
// validation: even with a valid storyID, the filename must not escape
// the per-story directory. sanitize.SafeJoin rejects ../.
func TestStore_ReadRejectsTraversalFilename(t *testing.T) {
	base := t.TempDir()
	s, err := NewStore(base)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Plant a sibling file outside the story dir — Read must not reach it.
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := s.Read("STORY-1", "../secret.txt"); err == nil {
		t.Fatal("expected SafeJoin to reject traversal filename")
	}
}

// TestStore_AppendCreatesFile_OnFirstCall covers the JSONL append path
// when the file doesn't exist yet — a frequent runtime case the first
// time a story emits a trace event.
func TestStore_AppendCreatesFile_OnFirstCall(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Append("STORY-X", TypeTraceEvents, map[string]any{"iteration": 1, "phase": "tool_call"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append("STORY-X", TypeTraceEvents, map[string]any{"iteration": 2, "phase": "completed"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	files, err := s.List("STORY-X")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], ".jsonl") {
		t.Errorf("expected single .jsonl file, got %v", files)
	}
}

// TestStore_WriteRawTracksDiffExtension confirms the special-case
// extension mapping for git diffs (.patch). Other types fall to .txt.
func TestStore_WriteRawTracksDiffExtension(t *testing.T) {
	base := t.TempDir()
	s, err := NewStore(base)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.WriteRaw("STORY-D", TypeGitDiff, "diff --git a/x b/x"); err != nil {
		t.Fatalf("WriteRaw diff: %v", err)
	}
	if err := s.WriteRaw("STORY-D", TypeRawLog, "log line"); err != nil {
		t.Fatalf("WriteRaw log: %v", err)
	}

	files, _ := s.List("STORY-D")
	hasPatch, hasTxt := false, false
	for _, f := range files {
		if strings.HasSuffix(f, ".patch") {
			hasPatch = true
		}
		if strings.HasSuffix(f, ".txt") {
			hasTxt = true
		}
	}
	if !hasPatch {
		t.Errorf("git diff should write .patch; got %v", files)
	}
	if !hasTxt {
		t.Errorf("agent log should write .txt; got %v", files)
	}
}
