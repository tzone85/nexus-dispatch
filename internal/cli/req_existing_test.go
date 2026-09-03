package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestReqCmd_ExistingRepo_DryRun plans against an existing Go codebase: the
// requirement is classified, the investigator runs, and both results are
// recorded before the plan is created.
func TestReqCmd_ExistingRepo_DryRun(t *testing.T) {
	env := setupTestEnv(t)
	workDir := t.TempDir()
	initTestRepo(t, workDir)
	// An "existing" codebase needs more than five source files and more than
	// ten commits (engine.ClassifyRepo).
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		name := filepath.Join(workDir, "file"+itoa(i)+".go")
		if err := os.WriteFile(name, []byte("package app\n\nvar V"+itoa(i)+" = "+itoa(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, workDir, "add", ".")
		gitRun(t, workDir, "commit", "-q", "-m", "commit "+itoa(i))
	}
	reqFile := filepath.Join(workDir, "requirement.md")
	if err := os.WriteFile(reqFile, []byte("Add a /health endpoint that returns 200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out, err := execCmd(t, newReqCmd(), env.Config, "--dry-run", "--file", reqFile)
	if err != nil {
		t.Fatalf("req --dry-run on existing repo: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Planning requirement: Add a /health endpoint that returns 200",
		"Detected: go codebase, requirement type: feature (confidence: 92%)",
		"Running codebase investigation...",
		"Plan created with",
		"Run 'nxd resume ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "greenfield") {
		t.Errorf("a repo with go.mod must not be classified as greenfield:\n%s", out)
	}

	classified, err := env.Events.List(state.EventFilter{Type: state.EventReqClassified})
	if err != nil || len(classified) != 1 {
		t.Fatalf("REQ_CLASSIFIED = %d (err=%v), want 1", len(classified), err)
	}
	p := state.DecodePayload(classified[0].Payload)
	if p["is_existing"] != true || p["req_type"] != "feature" {
		t.Errorf("classification payload = %v", p)
	}
	if strings.Contains(out, "Investigation complete") {
		if n, _ := env.Events.Count(state.EventFilter{Type: state.EventInvestigationCompleted}); n != 1 {
			t.Errorf("INVESTIGATION_COMPLETED = %d, want 1 after a successful investigation", n)
		}
	} else if !strings.Contains(out, "Warning: investigation failed") {
		t.Errorf("investigation must either complete or warn:\n%s", out)
	}
	planned, _ := env.Events.Count(state.EventFilter{Type: state.EventReqPlanned})
	if planned != 1 {
		t.Errorf("REQ_PLANNED = %d, want 1", planned)
	}
	stories, err := env.Proj.ListStories(state.StoryFilter{})
	if err != nil || len(stories) == 0 {
		t.Errorf("no stories projected (err=%v)", err)
	}
}

func TestReqCmd_RequirementSourceErrors(t *testing.T) {
	env := setupTestEnv(t)
	t.Run("positional and --file together", func(t *testing.T) {
		_, err := execCmd(t, newReqCmd(), env.Config, "--file", "x.md", "text")
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := execCmd(t, newReqCmd(), env.Config, "--file", filepath.Join(t.TempDir(), "nope.md"))
		if err == nil || !strings.Contains(err.Error(), "nope.md") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no requirement at all", func(t *testing.T) {
		_, err := execCmd(t, newReqCmd(), env.Config)
		if err == nil {
			t.Fatal("expected an error when no requirement text is given")
		}
	})
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
