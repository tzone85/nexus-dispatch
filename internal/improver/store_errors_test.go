package improver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveSuggestions_EmptyPathRejected covers the precondition guard.
// A loud error is better than silently writing to "" and creating a
// confusing entry in the cwd.
func TestSaveSuggestions_EmptyPathRejected(t *testing.T) {
	err := SaveSuggestions("", []Suggestion{{ID: "x"}})
	if err == nil {
		t.Fatal("expected error on empty path")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("error should mention empty path, got %v", err)
	}
}

// TestSaveSuggestions_WriteErrorIsWrapped exercises the os.WriteFile
// failure path by aiming at a directory that does not exist. The
// wrapping behaviour matters for operators reading nxd's logs — the
// "write suggestions" prefix tells them the cause is the persistence
// step, not the analyzer.
func TestSaveSuggestions_WriteErrorIsWrapped(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-dir", "improvements.json")
	err := SaveSuggestions(bogus, []Suggestion{{ID: "x"}})
	if err == nil {
		t.Fatal("expected error writing into nonexistent directory")
	}
	if !strings.Contains(err.Error(), "write suggestions") {
		t.Errorf("error should be wrapped with 'write suggestions', got %v", err)
	}
}

// TestLoadSuggestions_MalformedJSONIsError makes sure a corrupt
// improvements.json doesn't degrade the dashboard to an empty list —
// the operator should know their persisted state is broken.
func TestLoadSuggestions_MalformedJSONIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "improvements.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := LoadSuggestions(path)
	if err == nil {
		t.Fatal("expected decode error on malformed JSON")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode, got %v", err)
	}
}

// TestLoadSuggestions_PermissionErrorBubblesUp confirms unexpected IO
// errors (anything other than NotExist) propagate up rather than being
// silently swallowed. Uses a directory-as-file trick: opening a
// directory with os.ReadFile returns an error.
func TestLoadSuggestions_PermissionErrorBubblesUp(t *testing.T) {
	dir := t.TempDir()
	// Path points at a directory, not a file — ReadFile should error.
	_, err := LoadSuggestions(dir)
	if err == nil {
		t.Fatal("expected error reading a directory as a file")
	}
}
