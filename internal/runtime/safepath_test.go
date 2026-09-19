package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestErrReason: a *fs.PathError (wrapped or not) is reduced to its errno
// text, so the agent never reads the host path; any other error is returned
// as is.
func TestErrReason(t *testing.T) {
	pe := &fs.PathError{Op: "open", Path: "/home/operator/secret/work/x.go", Err: fs.ErrNotExist}
	if got := errReason(pe); got != fs.ErrNotExist {
		t.Errorf("PathError: want the bare errno %v, got %v", fs.ErrNotExist, got)
	}
	wrapped := fmt.Errorf("read: %w", pe)
	if got := errReason(wrapped); got != fs.ErrNotExist {
		t.Errorf("wrapped PathError: want the bare errno, got %v", got)
	}
	if strings.Contains(errReason(wrapped).Error(), "/home/operator") {
		t.Error("host path leaked through errReason")
	}
	plain := errors.New("plain failure")
	if got := errReason(plain); got != plain {
		t.Errorf("plain error must be returned unchanged, got %v", got)
	}
	le := &os.LinkError{Op: "symlink", Old: "/home/operator/a", New: "/home/operator/b", Err: fs.ErrExist}
	if got := errReason(le); got != fs.ErrExist {
		t.Errorf("LinkError: want the bare errno %v, got %v", fs.ErrExist, got)
	}
	se := os.NewSyscallError("open", fs.ErrPermission)
	if got := errReason(se); got != fs.ErrPermission {
		t.Errorf("SyscallError: want the bare errno %v, got %v", fs.ErrPermission, got)
	}
}

// TestSafePath_DanglingSymlinkInsideWorkDir pins a deliberate behaviour
// change: a dangling symlink is rejected even when it points INSIDE the work
// directory (latest -> ./build/out.txt, target missing). It cannot be proven
// safe without following it, and os.WriteFile would create the target
// through the link, so the sinks fail closed rather than guess.
func TestSafePath_DanglingSymlinkInsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.Symlink(filepath.Join("build", "out.txt"), filepath.Join(workDir, "latest")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	_, err := safePath("latest", workDir)
	if err == nil {
		t.Fatal("a dangling symlink must be rejected even when its target is inside the work directory")
	}
	if !strings.Contains(err.Error(), "cannot verify component") || strings.Contains(err.Error(), workDir) {
		t.Fatalf("rejection must say the component cannot be verified and carry no host path: %v", err)
	}
}

// TestExecWriteFile_RejectionIsLoggedWithStoryID: the agent-facing message
// hides the host path, but the operator log records the blocked attempt with
// the story that made it.
func TestExecWriteFile_RejectionIsLoggedWithStoryID(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	rt := newTestRuntime(t)
	rt.StoryID = "s-escape"
	res := rt.execWriteFile(makeToolCall("write_file", map[string]string{"path": "../outside.go", "content": "x"}), t.TempDir())
	if !res.IsError {
		t.Fatal("a traversal must be rejected")
	}
	if !strings.Contains(buf.String(), "s-escape") || !strings.Contains(buf.String(), "write_file") || !strings.Contains(buf.String(), "rejected") {
		t.Fatalf("rejection must be logged with the story ID and tool, got: %s", buf.String())
	}
}

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

// TestSafePath_NewFileUnderSymlinkedParent guards the new-file write path: a
// parent directory that is a symlink pointing outside the work directory must
// be rejected even though the final (new) file component does not exist yet.
// Before the fix, EvalSymlinks failed on the non-existent target and safePath
// returned the lexically-cleaned path, letting os.WriteFile follow the
// symlinked parent and escape the worktree.
func TestSafePath_NewFileUnderSymlinkedParent(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()

	// A symlinked directory inside workDir pointing outside it (as a git
	// checkout of an untrusted repo could contain).
	symlinkPath := filepath.Join(workDir, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	// Target file does NOT exist yet — this is the previously-unguarded path.
	_, err := safePath("escape/newfile.txt", workDir)
	if err == nil {
		t.Fatal("expected error for new file under symlinked-outside parent")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected 'traversal' in error, got: %v", err)
	}

	// Deeper: a not-yet-existing subdir under the symlinked parent must also
	// be rejected (the escape is via the existing symlinked ancestor).
	if _, err := safePath("escape/sub/newfile.txt", workDir); err == nil {
		t.Error("expected error for new file under symlinked-outside ancestor")
	}
}

// TestSafePath_NewFileInNewSubdir confirms the legitimate case still works:
// creating a new file in a not-yet-existing subdirectory of the work
// directory (no symlink involved) must be allowed.
func TestSafePath_NewFileInNewSubdir(t *testing.T) {
	workDir := t.TempDir()

	got, err := safePath("brand/new/file.txt", workDir)
	if err != nil {
		t.Fatalf("expected success for new file in new subdir, got: %v", err)
	}
	want := filepath.Join(workDir, "brand", "new", "file.txt")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSafePath_DanglingSymlinkFinalComponent: a repo ships
// `notes.txt -> <outside>/missing`. EvalSymlinks fails on the dangling link,
// so a naive "target does not exist yet" branch treats it as a new file and
// os.WriteFile (O_CREATE, no O_NOFOLLOW) then creates the target outside the
// worktree. safePath must fail closed and nothing may be created outside.
func TestSafePath_DanglingSymlinkFinalComponent(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "missing.txt")

	if err := os.Symlink(target, filepath.Join(workDir, "notes.txt")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	_, err := safePath("notes.txt", workDir)
	if err == nil {
		t.Fatal("expected error for dangling symlink as final component")
	}
	assertRejected(t, err, "notes.txt")
	if strings.Contains(err.Error(), outsideDir) || strings.Contains(err.Error(), workDir) {
		t.Errorf("error must not leak host paths to the agent: %v", err)
	}
	if _, statErr := os.Lstat(target); statErr == nil {
		t.Errorf("outside target %s must not exist", target)
	}
}

// TestSafePath_DanglingSymlinkParent: `cfg -> <outside>/missing` with a
// write to cfg/x.txt. safePath itself must reject it rather than relying on
// os.MkdirAll happening to fail on the dangling link.
func TestSafePath_DanglingSymlinkParent(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()

	if err := os.Symlink(filepath.Join(outsideDir, "missing"), filepath.Join(workDir, "cfg")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	_, err := safePath("cfg/x.txt", workDir)
	if err == nil {
		t.Fatal("expected error for new file under dangling symlinked parent")
	}
	assertRejected(t, err, "cfg")
}

// TestSafePath_SymlinkLoop: a -> b -> a inside the work directory can never be
// verified, so it fails closed instead of looping or being accepted.
func TestSafePath_SymlinkLoop(t *testing.T) {
	workDir := t.TempDir()
	if err := os.Symlink(filepath.Join(workDir, "b"), filepath.Join(workDir, "a")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := os.Symlink(filepath.Join(workDir, "a"), filepath.Join(workDir, "b")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	for _, rel := range []string{"a", "a/new.txt"} {
		_, err := safePath(rel, workDir)
		if err == nil {
			t.Errorf("%s: expected rejection for a symlink loop", rel)
			continue
		}
		assertRejected(t, err, "")
	}
}

// TestSafePath_WorkDirIsDanglingSymlink: the boundary case where the work
// directory itself cannot be verified must still reject without leaking the
// host path (the component is reported as ".").
func TestSafePath_WorkDirIsDanglingSymlink(t *testing.T) {
	parent := t.TempDir()
	workDir := filepath.Join(parent, "wt")
	if err := os.Symlink(filepath.Join(parent, "missing"), workDir); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	for _, rel := range []string{"a.txt", "."} {
		_, err := safePath(rel, workDir)
		if err == nil {
			t.Errorf("%s: expected rejection when workDir is a dangling symlink", rel)
			continue
		}
		assertRejected(t, err, "")
		if strings.Contains(err.Error(), parent) {
			t.Errorf("%s: error leaks the host path: %v", rel, err)
		}
	}
}

// TestSafePath_UnreadableAncestor: an ancestor without search permission
// cannot be verified; fail closed rather than treat EACCES as "new file".
func TestSafePath_UnreadableAncestor(t *testing.T) {
	if goruntime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission checks are bypassed for root and do not apply on Windows")
	}
	workDir := t.TempDir()
	locked := filepath.Join(workDir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, err := safePath("locked/sub/new.txt", workDir)
	if err == nil {
		t.Fatal("expected rejection for an ancestor that cannot be read")
	}
	assertRejected(t, err, "locked")
}

// TestSafePath_NonexistentWorkDir pins the bounded walk: when the work
// directory itself does not exist yet, nothing can be resolved and the
// lexically-contained path is accepted (the executor creates the worktree
// before any tool call, so this is only reachable from tests and misuse).
func TestSafePath_NonexistentWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "not-created-yet")

	got, err := safePath("a/b.txt", workDir)
	if err != nil {
		t.Fatalf("expected lexical acceptance for nonexistent workDir, got: %v", err)
	}
	if want := filepath.Join(workDir, "a", "b.txt"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := safePath("../escape.txt", workDir); err == nil {
		t.Error("lexical traversal must still be rejected when workDir does not exist")
	}
}

// TestExecWriteFile_SymlinkedParentEscape drives the real write_file sink
// (not just the safePath helper) through a symlinked parent and a dangling
// final symlink, and asserts nothing lands outside the work directory.
func TestExecWriteFile_SymlinkedParentEscape(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(workDir, "escape")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "dangling-target.txt"), filepath.Join(workDir, "notes.txt")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	rt := newTestRuntime(t)

	for _, rel := range []string{"escape/pwned.txt", "escape/sub/pwned.txt", "notes.txt"} {
		call := makeToolCall("write_file", map[string]string{"path": rel, "content": "pwned"})
		res := rt.execWriteFile(call, workDir)
		if !res.IsError {
			t.Errorf("write_file %q: expected IsError, got success: %s", rel, res.Content)
		}
		if !strings.Contains(res.Content, "path traversal blocked") && !strings.Contains(res.Content, "path rejected") {
			t.Errorf("write_file %q: expected a rejection error, got: %s", rel, res.Content)
		}
	}
	entries, err := os.ReadDir(outsideDir)
	if err != nil {
		t.Fatalf("read outside dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("write escaped the work directory: %d entries created in %s", len(entries), outsideDir)
	}
}

// TestExecEditFile_SymlinkedParentEscape drives the real edit_file sink
// through a symlinked parent and asserts the outside file is untouched.
func TestExecEditFile_SymlinkedParentEscape(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "config.txt")
	if err := os.WriteFile(outsideFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(workDir, "escape")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	rt := newTestRuntime(t)

	call := makeToolCall("edit_file", map[string]string{
		"path": "escape/config.txt", "old_text": "original", "new_text": "pwned",
	})
	res := rt.execEditFile(call, workDir)
	if !res.IsError || !strings.Contains(res.Content, "traversal") {
		t.Errorf("edit_file: expected traversal error, got IsError=%v: %s", res.IsError, res.Content)
	}
	got, _ := os.ReadFile(outsideFile)
	if string(got) != "original" {
		t.Errorf("outside file modified through symlinked parent: %q", got)
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

// TestExecReadFile_IOErrorOmitsHostPath: the read sink's I/O error (past
// safePath, which accepts a missing file) carries the errno text only.
func TestExecReadFile_IOErrorOmitsHostPath(t *testing.T) {
	workDir := t.TempDir()
	res := newTestRuntime(t).execReadFile(makeToolCall("read_file", map[string]string{"path": "missing.go"}), workDir)
	if !res.IsError {
		t.Fatal("reading a missing file must be an error")
	}
	if strings.Contains(res.Content, workDir) {
		t.Fatalf("host path leaked in read error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "read error: ") {
		t.Fatalf("expected the read sink's error, got: %s", res.Content)
	}
}

// TestExecWriteFile_IOErrorOmitsHostPath: the write sink's I/O error (a
// read-only parent directory, which safePath accepts) carries the errno text
// only. A regular file as parent (ENOTDIR) is rejected earlier, by safePath.
func TestExecWriteFile_IOErrorOmitsHostPath(t *testing.T) {
	if goruntime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, and Windows does not enforce them this way")
	}
	workDir := t.TempDir()
	locked := filepath.Join(workDir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	res := newTestRuntime(t).execWriteFile(makeToolCall("write_file", map[string]string{
		"path": "locked/new.go", "content": "package x",
	}), workDir)
	if !res.IsError {
		t.Fatal("writing into a read-only directory must be an error")
	}
	if strings.Contains(res.Content, workDir) {
		t.Fatalf("host path leaked in write error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "write error: ") {
		t.Fatalf("expected the write sink's error, got: %s", res.Content)
	}

	// ENOTDIR parent: safePath cannot verify "file.txt/x.go" and rejects it
	// before the sink runs — still without a host path.
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = newTestRuntime(t).execWriteFile(makeToolCall("write_file", map[string]string{
		"path": "file.txt/x.go", "content": "package x",
	}), workDir)
	if !res.IsError || strings.Contains(res.Content, workDir) {
		t.Fatalf("ENOTDIR parent: want a rejection without the host path, got error=%v %s", res.IsError, res.Content)
	}
}

// TestExecWriteFile_SymlinkEscape_LogsRealTarget: the agent's message never
// carries a host path, but the operator log names where the link pointed.
func TestExecWriteFile_SymlinkEscape_LogsRealTarget(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	workDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workDir, "vendor")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	rt := newTestRuntime(t)
	rt.StoryID = "s-escape-2"
	res := rt.execWriteFile(makeToolCall("write_file", map[string]string{"path": "vendor/x.go", "content": "x"}), workDir)
	if !res.IsError || strings.Contains(res.Content, outside) {
		t.Fatalf("agent message must reject without the host path, got error=%v %s", res.IsError, res.Content)
	}
	if !strings.Contains(buf.String(), outside) || !strings.Contains(buf.String(), "s-escape-2") {
		t.Fatalf("operator log must name the real target and the story, got: %s", buf.String())
	}
}

// TestSafePath_RejectionDetailHasNoRawNewline: the agent chooses the path, so
// a newline in it must not become a forged second line in the operator log —
// for a lexical escape and for a symlink whose name carries the newline.
func TestSafePath_RejectionDetailHasNoRawNewline(t *testing.T) {
	workDir := t.TempDir()
	forged := "../x\n2026/09/18 12:00:00 [native-runtime] s-9: all clear"
	if _, err := safePath(forged, workDir); err == nil {
		t.Fatal("a lexical escape must be rejected")
	} else if d := rejectionDetail(err); strings.Contains(d, "\n") || !strings.Contains(d, `\n`) {
		t.Fatalf("Detail must quote the path, got %q", d)
	}
	outside := t.TempDir()
	link := filepath.Join(workDir, "ln\nFAKE")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := safePath("ln\nFAKE/out.txt", workDir); err == nil {
		t.Fatal("a symlink escape must be rejected")
	} else if d := rejectionDetail(err); strings.Contains(d, "\n") {
		t.Fatalf("Detail must quote the symlink path, got %q", d)
	}
	plain := errors.New("first line\nsecond line")
	if d := rejectionDetail(plain); strings.Contains(d, "\n") {
		t.Fatalf("a plain error is quoted too, got %q", d)
	}
}
