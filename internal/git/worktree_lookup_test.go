package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWorktreeForBranch(t *testing.T) {
	// -z output: every attribute NUL-terminated, an empty one between records.
	porcelain := "worktree /repo\x00HEAD abc\x00branch refs/heads/main\x00\x00" +
		"worktree /state/worktrees/s-1\x00HEAD def\x00branch refs/heads/nxd/s-1\x00\x00" +
		"worktree /state/worktrees/s-10\x00HEAD 123\x00branch refs/heads/nxd/s-10\x00\x00" +
		"worktree /state/worktrees/gone\x00HEAD 789\x00branch refs/heads/nxd/gone\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /state/worktrees/held\x00HEAD 790\x00branch refs/heads/nxd/held\x00locked\x00\x00" +
		"worktree /state/worktrees/spaced path\x00HEAD 791\x00branch refs/heads/nxd/spaced\x00\x00" +
		"worktree /state/worktrees/line\nbreak\x00HEAD 792\x00branch refs/heads/nxd/lf\x00\x00" +
		"worktree /state/worktrees/det\x00HEAD 456\x00detached\x00\x00"
	cases := []struct {
		name, branch, want string
	}{
		{"main worktree", "main", "/repo"},
		{"external worktree", "nxd/s-1", "/state/worktrees/s-1"},
		{"prefix does not match", "nxd/s", ""},
		{"longer name", "nxd/s-10", "/state/worktrees/s-10"},
		{"prunable entry still reports its path", "nxd/gone", "/state/worktrees/gone"},
		{"locked entry", "nxd/held", "/state/worktrees/held"},
		{"path git would c-quote without -z", "nxd/spaced", "/state/worktrees/spaced path"},
		{"path with a newline in it", "nxd/lf", "/state/worktrees/line\nbreak"},
		{"unknown branch", "missing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseWorktreeForBranch(porcelain, tc.branch, true); got != tc.want {
				t.Errorf("%s: got %q want %q", tc.branch, got, tc.want)
			}
		})
	}
}

// TestWorktreeForBranch_NotARepoIsAnError: callers delete on "" (no
// worktree), so a moved or non-repo directory must be an error instead.
func TestWorktreeForBranch_NotARepoIsAnError(t *testing.T) {
	if _, err := WorktreeForBranch(t.TempDir(), "main"); err == nil {
		t.Error("a directory that is not a repository must be an error, not \"no worktree\"")
	}
}

// TestWorktreeForBranch_OldGitWithoutZ: `worktree list --porcelain -z` needs
// git >= 2.36. On an older git the switch is unknown and the command fails —
// failing closed there would break gc, archive and review, so the newline
// form is the fallback. The shim rejects -z and answers the plain form.
func TestWorktreeForBranch_OldGitWithoutZ(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"-z\" ] && { echo \"error: unknown switch \\`z'\" >&2; exit 129; }; done\n" +
		"printf '%s\\n' 'worktree /repo' 'HEAD abc' 'branch refs/heads/main' '' 'worktree /wt/s-1' 'HEAD def' 'branch refs/heads/nxd/s-1' ''\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")

	got, err := WorktreeForBranch(t.TempDir(), "nxd/s-1")
	if err != nil {
		t.Fatalf("an old git must not fail the lookup: %v", err)
	}
	if got != "/wt/s-1" {
		t.Fatalf("fallback parse: got %q, want /wt/s-1", got)
	}
}

// TestWorktreeForBranch_BothFormsFail: when the fallback fails too, the error
// is git's, not a silent "no worktree" — callers delete on "".
func TestWorktreeForBranch_BothFormsFail(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho 'fatal: not a git repository' >&2\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	if _, err := WorktreeForBranch(t.TempDir(), "main"); err == nil {
		t.Fatal("a git that fails both forms must be an error, not \"no worktree\"")
	}
}

// TestRejectsZ: the fallback fires for a git too old to know -z and for
// nothing else. The first string is what git 2.34 prints.
func TestRejectsZ(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"exit status 129 (error: unknown switch `z')", true},
		{"exit status 129 (error: unknown option: -z)", true},
		{"exit status 128 (fatal: not a git repository)", false},
		{"chdir /gone: no such file or directory", false},
		{"exit status 128 (fatal: detected dubious ownership)", false},
	} {
		if got := rejectsZ(errors.New(tc.msg)); got != tc.want {
			t.Errorf("rejectsZ(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestWorktreeForBranch_RealFailureNotRetried: a failure that is not "this
// git does not know -z" is reported as it is. Retrying would cost a second
// git call and replace git's answer with the same answer about a different
// command line.
func TestWorktreeForBranch_RealFailureNotRetried(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho x >> " + calls + "\necho 'fatal: not a git repository' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")

	_, err := WorktreeForBranch(t.TempDir(), "main")
	if err == nil {
		t.Fatal("a git that fails for a real reason must be an error, not \"no worktree\"")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("git's own message must survive, got %v", err)
	}
	b, rerr := os.ReadFile(calls)
	if rerr != nil {
		t.Fatalf("read the call log: %v", rerr)
	}
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("git must be run once for a real failure, ran %d times", n)
	}
}
