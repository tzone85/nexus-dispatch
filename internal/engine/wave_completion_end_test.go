package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newOriginAndClone creates a bare origin with one commit on main and a
// clone of it. Returns (origin, clone).
func newOriginAndClone(t *testing.T) (string, string) {
	t.Helper()
	seed := t.TempDir()
	gitIn(t, seed, "init", "-b", "main")
	gitIn(t, seed, "config", "user.email", "t@t.t")
	gitIn(t, seed, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-m", "base")

	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, seed, "clone", "--bare", seed, origin)

	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, seed, "clone", origin, clone)
	gitIn(t, clone, "config", "user.email", "t@t.t")
	gitIn(t, clone, "config", "user.name", "t")
	return origin, clone
}

// pushCommitToOrigin adds a commit to origin/main through a second clone so
// the first clone is behind.
func pushCommitToOrigin(t *testing.T, origin, file string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", origin, other)
	gitIn(t, other, "config", "user.email", "t@t.t")
	gitIn(t, other, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(other, file), []byte("upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-m", "upstream "+file)
	gitIn(t, other, "push", "origin", "main")
}

func TestGitPullWithStash(t *testing.T) {
	t.Run("clean tree fast-forwards", func(t *testing.T) {
		origin, clone := newOriginAndClone(t)
		pushCommitToOrigin(t, origin, "upstream.txt")
		before := gitIn(t, clone, "rev-parse", "HEAD")

		gitPullWithStash(clone, "main")

		if after := gitIn(t, clone, "rev-parse", "HEAD"); after == before {
			t.Fatal("HEAD did not advance after pull")
		}
		if _, err := os.Stat(filepath.Join(clone, "upstream.txt")); err != nil {
			t.Errorf("upstream file missing after pull: %v", err)
		}
	})

	t.Run("dirty tree is stashed, pulled and restored", func(t *testing.T) {
		origin, clone := newOriginAndClone(t)
		pushCommitToOrigin(t, origin, "upstream.txt")
		if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("local edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone, "scratch.txt"), []byte("untracked\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := gitIn(t, clone, "rev-parse", "HEAD")

		gitPullWithStash(clone, "main")

		if after := gitIn(t, clone, "rev-parse", "HEAD"); after == before {
			t.Fatal("HEAD did not advance: dirty tree should have been stashed around the pull")
		}
		got, _ := os.ReadFile(filepath.Join(clone, "base.txt"))
		if string(got) != "local edit\n" {
			t.Errorf("local modification lost after stash pop: %q", got)
		}
		if _, err := os.Stat(filepath.Join(clone, "scratch.txt")); err != nil {
			t.Errorf("untracked file lost after stash pop: %v", err)
		}
		if out := gitIn(t, clone, "stash", "list"); out != "" {
			t.Errorf("stash not popped: %q", out)
		}
	})

	t.Run("local-only repo logs the failed pull and keeps the tree", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-pull")
		head := gitIn(t, wt, "rev-parse", "HEAD")
		gitPullWithStash(wt, "main")
		if gitIn(t, wt, "rev-parse", "HEAD") != head {
			t.Error("HEAD changed on a repo with no origin")
		}
	})
}

func TestPullBaseAfterMerge_RemovesArtifactsAndPulls(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	pushCommitToOrigin(t, origin, "upstream.txt")
	for _, f := range []string{"WAVE_CONTEXT.md", "REQUIREMENT.md", ".nxd-fix-gaps.md"} {
		if err := os.WriteFile(filepath.Join(clone, f), []byte("nxd artifact\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := gitIn(t, clone, "rev-parse", "HEAD")

	pullBaseAfterMerge(clone, "")

	for _, f := range []string{"WAVE_CONTEXT.md", "REQUIREMENT.md", ".nxd-fix-gaps.md"} {
		if _, err := os.Stat(filepath.Join(clone, f)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed before the pull (err=%v)", f, err)
		}
	}
	if gitIn(t, clone, "rev-parse", "HEAD") == before {
		t.Error("base branch was not pulled (branch auto-detected as main)")
	}
	gi, err := os.ReadFile(filepath.Join(clone, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), ".nxd-prompts/") {
		t.Errorf(".gitignore should list NXD artifacts: %q (err=%v)", gi, err)
	}
}

func TestPullBaseAfterMerge_UnknownBranchIsNoop(t *testing.T) {
	_, clone := newOriginAndClone(t)
	head := gitIn(t, clone, "rev-parse", "HEAD")
	pullBaseAfterMerge(clone, "no-such-branch")
	if gitIn(t, clone, "rev-parse", "HEAD") != head {
		t.Error("HEAD changed although the base branch does not exist")
	}
}

func TestCleanupDanglingBranches(t *testing.T) {
	seed := func(t *testing.T, es state.EventStore, ps state.ProjectionStore) {
		t.Helper()
		seedWaveReq(t, es, ps, "REQ-CLEAN", map[string]string{"s-merged": "merged", "s-draft": "draft", "s-blocked": "draft"})
	}

	t.Run("disabled by config leaves branches alone", func(t *testing.T) {
		es, ps := pipelineStores(t)
		seed(t, es, ps)
		_, clone := newOriginAndClone(t)
		gitIn(t, clone, "branch", "nxd/s-draft")
		cfg := config.DefaultConfig()
		cfg.Cleanup.DeleteDanglingBranches = false
		m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)
		m.cleanupDanglingBranches("REQ-CLEAN", clone)
		if !nxdgit.BranchExists(clone, "nxd/s-draft") {
			t.Error("branch deleted although cleanup is disabled")
		}
	})

	t.Run("deletes local and remote branches of unmerged stories", func(t *testing.T) {
		es, ps := pipelineStores(t)
		seed(t, es, ps)
		origin, clone := newOriginAndClone(t)
		gitIn(t, clone, "branch", "nxd/s-draft")
		gitIn(t, clone, "push", "origin", "nxd/s-draft")
		gitIn(t, clone, "branch", "nxd/s-merged") // merged story's branch must be left alone
		cfg := config.DefaultConfig()
		cfg.Cleanup.DeleteDanglingBranches = true
		cfg.Merge.BaseBranch = "main"
		m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)

		m.cleanupDanglingBranches("REQ-CLEAN", clone)

		if nxdgit.BranchExists(clone, "nxd/s-draft") {
			t.Error("local dangling branch nxd/s-draft still exists")
		}
		if out := gitIn(t, origin, "branch", "--list", "nxd/s-draft"); out != "" {
			t.Errorf("remote dangling branch still exists on origin: %q", out)
		}
		if !nxdgit.BranchExists(clone, "nxd/s-merged") {
			t.Error("merged story's branch must not be touched")
		}
		if !nxdgit.BranchExists(clone, "main") {
			t.Error("base branch must never be deleted")
		}
	})

	t.Run("missing requirement lists nothing", func(t *testing.T) {
		es, ps := pipelineStores(t)
		_, clone := newOriginAndClone(t)
		gitIn(t, clone, "branch", "nxd/s-draft")
		cfg := config.DefaultConfig()
		cfg.Cleanup.DeleteDanglingBranches = true
		m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)
		m.cleanupDanglingBranches("REQ-UNKNOWN", clone)
		if !nxdgit.BranchExists(clone, "nxd/s-draft") {
			t.Error("branch of another requirement was deleted")
		}
	})
}

func TestCompleteRequirement_CompletionGate(t *testing.T) {
	newFixture := func(t *testing.T, verify verifyFunc) (*Monitor, state.EventStore, string) {
		t.Helper()
		es, ps := pipelineStores(t)
		seedWaveReq(t, es, ps, "REQ-GATE", map[string]string{"s-a": "merged"})
		_, repo := newOriginAndClone(t)
		cfg := config.DefaultConfig()
		cfg.Merge.BaseBranch = "main"
		m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)
		gate := NewCompletionGate(nil, "m", 100, 1, "main", es, ps)
		gate.verify = verify
		gate.pull = func(string, string) {}
		m.SetCompletionGate(gate)
		return m, es, repo
	}
	stories := []state.Story{{ID: "s-a", Title: "A", Status: "merged"}}
	rc := &RunContext{ReqID: "REQ-GATE", DAG: graph.New()}

	t.Run("green mainline completes", func(t *testing.T) {
		m, es, repo := newFixture(t, func(context.Context, string, int) VerificationResult {
			return VerificationResult{BuildPasses: true}
		})
		m.completeRequirement(context.Background(), rc, repo, stories)
		done, _ := es.List(state.EventFilter{Type: state.EventReqCompleted})
		if len(done) != 1 || state.DecodePayload(done[0].Payload)["id"] != "REQ-GATE" {
			t.Fatalf("REQ_COMPLETED = %+v", done)
		}
		if blocked, _ := es.List(state.EventFilter{Type: state.EventReqBlocked}); len(blocked) != 0 {
			t.Error("green mainline must not emit REQ_BLOCKED")
		}
	})

	t.Run("red mainline without fixer blocks and records the gaps", func(t *testing.T) {
		m, es, repo := newFixture(t, func(context.Context, string, int) VerificationResult {
			return VerificationResult{BuildPasses: false, Gaps: []VerificationGap{{Category: "build", Severity: "critical", Detail: "undefined: Foo"}}}
		})
		m.completeRequirement(context.Background(), rc, repo, stories)
		if done, _ := es.List(state.EventFilter{Type: state.EventReqCompleted}); len(done) != 0 {
			t.Fatal("red mainline must not emit REQ_COMPLETED")
		}
		blocked, _ := es.List(state.EventFilter{Type: state.EventReqBlocked})
		if len(blocked) != 1 || state.DecodePayload(blocked[0].Payload)["id"] != "REQ-GATE" {
			t.Fatalf("REQ_BLOCKED = %+v", blocked)
		}
		gaps, err := os.ReadFile(filepath.Join(repo, ".nxd-fix-gaps.md"))
		if err != nil || !strings.Contains(string(gaps), "undefined: Foo") {
			t.Errorf(".nxd-fix-gaps.md = %q (err=%v)", gaps, err)
		}
	})
}

func TestCompleteRequirement_GeneratesDocsBeforeCompleting(t *testing.T) {
	es, ps := pipelineStores(t)
	seedWaveReq(t, es, ps, "REQ-DOCS", map[string]string{"s-a": "merged"})
	_, repo := newOriginAndClone(t)
	cfg := config.DefaultConfig()
	cfg.Merge.BaseBranch = "main"
	m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)
	// One README reply; the follow-up diagram / ADR prompts run out of
	// replies and are skipped as non-fatal.
	m.SetDocGenerator(llm.NewReplayClient(llm.CompletionResponse{Content: "# Widget\n\nThe widget service.\n"}), "doc-model")
	gate := NewCompletionGate(nil, "m", 100, 0, "main", es, ps)
	gate.verify = func(context.Context, string, int) VerificationResult { return VerificationResult{BuildPasses: true} }
	gate.pull = func(string, string) {}
	m.SetCompletionGate(gate)

	m.completeRequirement(context.Background(), &RunContext{ReqID: "REQ-DOCS", DAG: graph.New()}, repo, []state.Story{{ID: "s-a", Title: "Build the widget", Status: "merged"}})

	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || !strings.HasPrefix(string(readme), "# Widget") || !strings.Contains(string(readme), "NXD") {
		t.Fatalf("README.md = %q (err=%v)", readme, err)
	}
	if subject := gitIn(t, repo, "log", "-1", "--pretty=%s"); !strings.HasPrefix(subject, "docs: update README") {
		t.Errorf("docs were not committed; last subject = %q", subject)
	}
	if done, _ := es.List(state.EventFilter{Type: state.EventReqCompleted}); len(done) != 1 {
		t.Errorf("REQ_COMPLETED = %d, want 1", len(done))
	}
}
