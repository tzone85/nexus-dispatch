package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/runtime"
)

// The review's investigator bypasses: allowlisted binaries reaching outside
// the repository through absolute paths, ~, or a filesystem walk from /.
func TestInvestigator_RunCommand_RejectsRepositoryEscapes(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"cat", "head", "tail", "find", "grep", "ls"})
	repo := t.TempDir()

	cases := []string{
		"cat /etc/passwd",
		"cat ~/.aws/credentials",
		"find / -name *.pem",
		"head -n 5 /etc/shadow",
		"tail ../../.bashrc",
		"grep -r password /home",
		"ls ~",
		"find . -name *.go -exec cat {} +",
	}
	for _, cmd := range cases {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		out := inv.handleRunCommand(context.Background(), repo, args)
		if !strings.HasPrefix(out, "error: command not in allowlist") {
			t.Errorf("%q must be denied, got %q", cmd, out)
		}
	}
	// Inside the repo the same tools work.
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("hello repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"command": "cat notes.txt"})
	if out := inv.handleRunCommand(context.Background(), repo, args); !strings.Contains(out, "hello repo") {
		t.Errorf("cat inside repo: %q", out)
	}
	args, _ = json.Marshal(map[string]string{"command": "find . -name notes.txt"})
	if out := inv.handleRunCommand(context.Background(), repo, args); !strings.Contains(out, "notes.txt") {
		t.Errorf("find inside repo: %q", out)
	}
}

func TestInvestigator_RunCommand_EmptyAllowlistDeniesEverything(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	args, _ := json.Marshal(map[string]string{"command": "ls"})
	out := inv.handleRunCommand(context.Background(), t.TempDir(), args)
	if !strings.Contains(out, "not in allowlist") || !strings.Contains(out, "allowlist is empty") {
		t.Errorf("out = %q", out)
	}
}

func TestInvestigator_RunCommand_BadArgs(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	if out := inv.handleRunCommand(context.Background(), t.TempDir(), json.RawMessage(`{`)); !strings.HasPrefix(out, "error: invalid run_command arguments") {
		t.Errorf("out = %q", out)
	}
}

// recordingSandbox asserts the investigator routes run_command through the
// injected CommandSandbox and reports non-zero exits / errors.
type recordingSandbox struct {
	argv    []string
	workDir string
	res     runtime.ExecResult
	err     error
}

func (r *recordingSandbox) Exec(_ context.Context, workDir string, argv []string, _ time.Duration) (runtime.ExecResult, error) {
	r.argv, r.workDir = argv, workDir
	return r.res, r.err
}
func (r *recordingSandbox) Name() string { return "recording" }

func TestInvestigator_RunCommand_UsesSandbox(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"go test"})
	sb := &recordingSandbox{res: runtime.ExecResult{Stdout: "ok"}}
	inv.SetSandbox(sb)
	repo := t.TempDir()

	args, _ := json.Marshal(map[string]string{"command": `go test ./... -run "TestA B"`})
	out := inv.handleRunCommand(context.Background(), repo, args)
	if out != "ok" {
		t.Errorf("out = %q", out)
	}
	if sb.workDir != repo || strings.Join(sb.argv, "|") != "go|test|./...|-run|TestA B" {
		t.Errorf("sandbox got workDir=%q argv=%q", sb.workDir, sb.argv)
	}

	sb.res = runtime.ExecResult{Stderr: "FAIL", ExitCode: 1}
	if out := inv.handleRunCommand(context.Background(), repo, args); !strings.Contains(out, "FAIL") || !strings.Contains(out, "exit error: exit status 1") {
		t.Errorf("non-zero exit: %q", out)
	}
	sb.err = context.DeadlineExceeded
	if out := inv.handleRunCommand(context.Background(), repo, args); !strings.Contains(out, "exit error: context deadline exceeded") {
		t.Errorf("error: %q", out)
	}
	sb.err = nil
	sb.res = runtime.ExecResult{Stdout: strings.Repeat("x", 5000)}
	if out := inv.handleRunCommand(context.Background(), repo, args); !strings.Contains(out, "truncated, output exceeds 4000 chars") {
		t.Errorf("truncation missing: %d bytes", len(out))
	}
}

func TestInvestigator_ReadFile_SymlinkOutsideRepoDenied(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A "committed" symlink inside the repo pointing outside it.
	if err := os.Symlink(secret, filepath.Join(repo, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ok.txt"), []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv := NewInvestigator(nil, "", 0)
	for _, p := range []string{"link.txt", "linkdir/secret.txt", "../" + filepath.Base(outside) + "/secret.txt", secret, "~/x"} {
		args, _ := json.Marshal(map[string]string{"path": p})
		out := inv.handleReadFile(repo, args)
		if out != "error: path traversal detected — access denied" {
			t.Errorf("read_file(%q) = %q, want access denied", p, out)
		}
	}
	args, _ := json.Marshal(map[string]string{"path": "ok.txt"})
	if out := inv.handleReadFile(repo, args); out != "fine" {
		t.Errorf("legit read = %q", out)
	}
	args, _ = json.Marshal(map[string]string{"path": "missing.txt"})
	if out := inv.handleReadFile(repo, args); !strings.HasPrefix(out, "error: open") {
		t.Errorf("missing file = %q", out)
	}
}

func TestResolveRepoPath_InternalSymlinkAllowed(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "real.txt"), filepath.Join(repo, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := resolveRepoPath(repo, "alias.txt")
	if err != nil {
		t.Fatalf("internal symlink must be allowed: %v", err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(repo, "real.txt"))
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	if _, err := resolveRepoPath(repo, ""); err != nil {
		t.Errorf("repo root itself must resolve: %v", err)
	}
}
