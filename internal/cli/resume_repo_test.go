package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestResolveRepoDir(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	// A symlink to repoA must count as the same repository.
	linkToA := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repoA, linkToA); err != nil {
		t.Skip("symlinks unavailable")
	}

	tests := []struct {
		name     string
		reqRepo  string
		cwd      string
		repoFlag string
		want     string
		wantErr  string
	}{
		{"matches cwd", repoA, repoA, "", repoA, ""},
		{"matches via symlink", repoA, linkToA, "", linkToA, ""},
		{"legacy req without repo_path uses cwd", "", repoB, "", repoB, ""},
		{"legacy req with --repo", "", repoB, repoA, repoA, ""},
		{"mismatch refused", repoA, repoB, "", "", "belongs to " + repoA + "; run from that repo or pass --repo " + repoA},
		{"--repo pointing at the right repo from elsewhere", repoA, repoB, repoA, repoA, ""},
		{"--repo pointing at the wrong repo", repoA, repoB, repoB, "", "but --repo is " + repoB},
		{"--repo not a directory", repoA, repoB, filepath.Join(repoB, "nope"), "", "is not a directory"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRepoDir(state.Requirement{ID: "R1", RepoPath: tc.reqRepo}, tc.cwd, tc.repoFlag)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("repoDir = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSamePath_UnresolvableFallsBackToClean(t *testing.T) {
	if !samePath("/no/such/a/../b", "/no/such/b") {
		t.Error("unresolvable paths should compare cleaned")
	}
	if samePath("/no/such/a", "/no/such/b") {
		t.Error("different paths must not match")
	}
}

// Defect 5(d): resume refuses when the requirement belongs to another repo.
func TestRunResume_RefusesForeignRepo(t *testing.T) {
	env := setupTestEnv(t)
	otherRepo := t.TempDir()
	seedTestReq(t, env, "REQ-X", "elsewhere", otherRepo)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	_, err := execCmd(t, newResumeCmd(), env.Config, "REQ-X")
	if err == nil || !strings.Contains(err.Error(), "belongs to "+otherRepo) || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("expected repo mismatch refusal, got %v", err)
	}
	// Nothing was emitted and the lock was released.
	if n, _ := env.Events.Count(state.EventFilter{Type: state.EventReqResumed}); n != 0 {
		t.Error("no REQ_RESUMED should be emitted when refused")
	}
	if l, err := engine.TryAcquireLock(filepath.Join(env.Dir, ".nxd")); err != nil {
		t.Errorf("lock must be released after refusal: %v", err)
	} else {
		l.Release()
	}
}

// Defect 4(c)/7: resume takes the lock first and fails fast when a pipeline
// is already running instead of rebuilding under it.
func TestRunResume_FailsWhenPipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-1", "t", "/tmp")
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	_, err = execCmd(t, newResumeCmd(), env.Config, "REQ-1")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected lock error, got %v", err)
	}
}

// --force refuses to clear a lock whose holder is alive.
func TestRunResume_ForceRefusesLiveHolder(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "REQ-1", "t", "/tmp")
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	_, err = execCmd(t, newResumeCmd(), env.Config, "REQ-1", "--force")
	if err == nil || !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("expected refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(env.Dir, ".nxd", "nxd.lock")); statErr != nil {
		t.Error("live holder's lock must not be removed")
	}
}

// --force clears a stale lock (dead pid) and proceeds to the next check.
func TestRunResume_ForceClearsStaleLock(t *testing.T) {
	env := setupTestEnv(t)
	lockPath := filepath.Join(env.Dir, ".nxd", "nxd.lock")
	os.WriteFile(lockPath, []byte(`{"pid":999999,"started_at":"2026-01-01T00:00:00Z"}`), 0o644)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	_, err := execCmd(t, newResumeCmd(), env.Config, "REQ-NONE", "--force")
	// Lock cleared → we get past locking and fail on the missing requirement.
	if err == nil || !strings.Contains(err.Error(), "requirement not found") {
		t.Fatalf("expected 'requirement not found' after force-clear, got %v", err)
	}
}

func TestRunResume_ForceBadConfig(t *testing.T) {
	_, err := execCmd(t, newResumeCmd(), filepath.Join(t.TempDir(), "nope.yaml"), "REQ", "--force")
	if err == nil {
		t.Fatal("expected config error")
	}
}

// Defect 8: the shared pipeline client is semaphore-wrapped with the native
// runtime's concurrency.
func TestWrapSharedClient(t *testing.T) {
	cfg := config.DefaultConfig()
	if wrapSharedClient(cfg, nil) != nil {
		t.Error("nil stays nil")
	}
	inner := llm.NewDryRunClient(0)
	got := wrapSharedClient(cfg, inner)
	if _, ok := got.(*llm.SemaphoreClient); !ok {
		t.Fatalf("expected *llm.SemaphoreClient, got %T", got)
	}
	// Concurrency comes from the native runtime config.
	rt := cfg.Runtimes["gemma"]
	rt.Concurrency = 3
	cfg.Runtimes["gemma"] = rt
	if _, ok := wrapSharedClient(cfg, inner).(*llm.SemaphoreClient); !ok {
		t.Error("expected semaphore wrapping with configured concurrency")
	}
}

// Wiring guard (source-scan, like the other resume wiring tests): the shared
// client must actually pass through wrapSharedClient and the repo check must
// gate the run.
func TestResume_WiresSharedSemaphoreAndRepoCheck(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		"llmClient = wrapSharedClient(s.Config, llmClient)",
		"resolveRepoDir(req, cwd, repoFlag)",
		"loadStoresLocked(cfgPath)",
		"engine.ForceClearLock(",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must contain %q", want)
		}
	}
	for _, banned := range []string{"nativeClient, _ = buildLLMClient", "artStore, _ :=", "sb, _ :=", "os.Remove(filepath.Join(stateDir, \"nxd.lock\"))"} {
		if strings.Contains(code, banned) {
			t.Errorf("resume.go must not swallow errors / remove locks unconditionally: found %q", banned)
		}
	}
}
