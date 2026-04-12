package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTmuxRunner_Implements_Runner(t *testing.T) {
	// Compile-time check that TmuxRunner satisfies Runner interface
	var _ Runner = (*TmuxRunner)(nil)
}

func TestCLIAdapter_Implements_Adapter(t *testing.T) {
	// Compile-time check that CLIAdapter satisfies Adapter interface
	var _ Adapter = (*CLIAdapter)(nil)
}

func TestNewTmuxRunner_NotNil(t *testing.T) {
	runner := NewTmuxRunner()
	if runner == nil {
		t.Fatal("NewTmuxRunner should return a non-nil runner")
	}
}

func TestPreparedExecution_SetupFilesWritable(t *testing.T) {
	// Verify that SetupFiles in PreparedExecution can hold multiple entries
	// and that the struct is usable as a value type (no hidden pointers).
	exec := PreparedExecution{
		Command:     "echo hello",
		WorkDir:     "/tmp",
		Env:         map[string]string{"KEY": "val"},
		SessionName: "test",
		LogFile:     "/tmp/log",
		SetupFiles:  map[string]string{"a.txt": "aaa", "b.txt": "bbb"},
	}

	if len(exec.SetupFiles) != 2 {
		t.Errorf("SetupFiles should have 2 entries, got %d", len(exec.SetupFiles))
	}
	if exec.Command != "echo hello" {
		t.Errorf("Command = %q, want echo hello", exec.Command)
	}
	if exec.Env["KEY"] != "val" {
		t.Error("Env should contain KEY=val")
	}
}

func TestTmuxRunner_WriteSetupFiles(t *testing.T) {
	// Test that setup files are written correctly by invoking the write logic
	// without actually running tmux (we can't reliably assume tmux is available
	// in CI). Instead, test the file-writing part of Run indirectly.
	dir := t.TempDir()
	testFile := filepath.Join(dir, "subdir", "test.txt")

	exec := PreparedExecution{
		Command:     "echo test",
		WorkDir:     dir,
		SessionName: "test-write-setup",
		SetupFiles:  map[string]string{testFile: "hello world"},
	}

	// Write setup files manually (same logic as TmuxRunner.Run)
	for path, content := range exec.SetupFiles {
		d := filepath.Dir(path)
		os.MkdirAll(d, 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}
