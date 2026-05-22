package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConflictError_Error(t *testing.T) {
	err := &ConflictError{Output: "CONFLICT in main.go"}
	s := err.Error()
	if s != "merge conflict: CONFLICT in main.go" {
		t.Errorf("Error() = %q", s)
	}
}

func TestIsConflict_True(t *testing.T) {
	err := &ConflictError{Output: "test"}
	if !IsConflict(err) {
		t.Error("expected IsConflict to return true for *ConflictError")
	}
}

func TestIsConflict_False(t *testing.T) {
	if IsConflict(errors.New("not a conflict")) {
		t.Error("expected IsConflict to return false for non-ConflictError")
	}
}

func TestIsConflict_Nil(t *testing.T) {
	if IsConflict(nil) {
		t.Error("expected IsConflict to return false for nil")
	}
}

func TestIsConflictInternal_CONFLICT(t *testing.T) {
	if !isConflict("error: CONFLICT (content): merge conflict in foo.go") {
		t.Error("expected true for CONFLICT keyword")
	}
}

func TestIsConflictInternal_CouldNotApply(t *testing.T) {
	if !isConflict("error: could not apply abc123") {
		t.Error("expected true for 'could not apply'")
	}
}

func TestIsConflictInternal_ResolveAll(t *testing.T) {
	if !isConflict("Resolve all conflicts manually") {
		t.Error("expected true for 'Resolve all conflicts'")
	}
}

func TestIsConflictInternal_NoConflict(t *testing.T) {
	if isConflict("Already up to date.") {
		t.Error("expected false for clean rebase output")
	}
}

// --------------------------------------------------------------------------
// IsBinaryConflict tests
// --------------------------------------------------------------------------

func TestIsBinaryConflict_TextFile(t *testing.T) {
	dir := helperInitRepo(t)

	// The repo was initialised with file.txt (text) — it is not binary.
	isBin, err := IsBinaryConflict(dir, "file.txt")
	if err != nil {
		t.Fatalf("IsBinaryConflict: %v", err)
	}
	if isBin {
		t.Error("expected text file to NOT be detected as binary")
	}
}

func TestIsBinaryConflict_BinaryFile(t *testing.T) {
	dir := helperInitRepo(t)

	// Write a file with null bytes and commit it so git knows about it.
	binData := []byte{'B', 'I', 'N', 0x00, 0x01, 0x02}
	binPath := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(binPath, binData, 0644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	helperRun(t, dir, "git", "add", "binary.bin")
	helperRun(t, dir, "git", "commit", "-m", "add binary file")

	// Modify it so there's a diff — git numstat will mark it as binary (-\t-\t).
	if err := os.WriteFile(binPath, append(binData, 0xFF), 0644); err != nil {
		t.Fatalf("modify binary: %v", err)
	}

	isBin, err := IsBinaryConflict(dir, "binary.bin")
	if err != nil {
		t.Fatalf("IsBinaryConflict: %v", err)
	}
	if !isBin {
		t.Error("expected binary file (with null bytes + modification) to be detected as binary")
	}
}

func TestIsBinaryConflict_NewlyAddedBinaryFile(t *testing.T) {
	// When a file has never been committed (not in HEAD), numstat returns
	// empty — IsBinaryConflict should fall back to SniffBinary.
	dir := helperInitRepo(t)

	binData := []byte{'E', 'L', 'F', 0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(filepath.Join(dir, "server"), binData, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Do NOT commit — simulate a newly-added unmerged file.
	isBin, err := IsBinaryConflict(dir, "server")
	if err != nil {
		t.Fatalf("IsBinaryConflict: %v", err)
	}
	if !isBin {
		t.Error("expected newly-added binary file (with null byte) to be detected as binary via sniff fallback")
	}
}

func TestIsBinaryConflict_InvalidDir(t *testing.T) {
	// Invalid directory: should return true (fail-safe), no panic.
	isBin, _ := IsBinaryConflict("/nonexistent/path/xyz", "file.txt")
	if !isBin {
		t.Error("IsBinaryConflict on invalid dir should fail safe and return true")
	}
}

// --------------------------------------------------------------------------
// SniffBinary tests
// --------------------------------------------------------------------------

func TestSniffBinary_NullByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{0x7F, 'E', 'L', 'F', 0x00}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := SniffBinary(path)
	if err != nil {
		t.Fatalf("SniffBinary: %v", err)
	}
	if !got {
		t.Error("expected true for file with null byte")
	}
}

func TestSniffBinary_PlainText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "text.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := SniffBinary(path)
	if err != nil {
		t.Fatalf("SniffBinary: %v", err)
	}
	if got {
		t.Error("expected false for plain-text file")
	}
}

func TestSniffBinary_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := SniffBinary(path)
	if err != nil {
		t.Fatalf("SniffBinary on empty file: %v", err)
	}
	if got {
		t.Error("expected false for empty file")
	}
}

func TestSniffBinary_NonExistentFile(t *testing.T) {
	_, err := SniffBinary("/nonexistent/path/file.bin")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
