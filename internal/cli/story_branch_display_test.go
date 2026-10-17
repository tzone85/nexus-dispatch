package cli

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// setStoryStatus moves a story to a status no seeded event produces here,
// straight into the projection: the row this test needs — in flight, with no
// projected branch — is what an older nxd resume left behind, not something
// the current pipeline emits.
func setStoryStatus(t *testing.T, env *testEnv, storyID, status string) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(env.Dir, ".nxd", "nxd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE stories SET status = ? WHERE id = ?`, status, storyID); err != nil {
		t.Fatal(err)
	}
}

// TestStatus_InFlightRowWithoutProjectedBranch: state.StoryBranch's fallback
// exists for a row an older resume projected without a branch. status will
// not show a branch for it — the projection is also the evidence the story
// started — and --json reports "". What review does with the same row is the
// next test; the two must not contradict each other.
func TestStatus_InFlightRowWithoutProjectedBranch(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Req", env.Dir)
	seedTestStory(t, env, "s-1", "req-00100", "Story", 1)
	setStoryStatus(t, env, "s-1", "in_progress")

	out, err := execCmd(t, newStatusCmd(), env.Config, "--all")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if strings.Contains(out, "Branch:") {
		t.Fatalf("a row with no projected branch and no merge has no branch line, got:\n%s", out)
	}

	jsonOut, err := execCmd(t, newStatusCmd(), env.Config, "--all", "--json")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, jsonOut)
	}
	if strings.Contains(jsonOut, "nxd/s-1") {
		t.Fatalf("--json must not invent a branch for such a row, got:\n%s", jsonOut)
	}
}

// TestReviewStory_NotStarted_BranchLine: review acts on the canonical name,
// so it says what it would act on — and that the story never started, which
// is why status shows nothing for the same row.
func TestReviewStory_NotStarted_BranchLine(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "req-00100", "Req", repo)
	seedTestStory(t, env, "s-1", "req-00100", "Story", 1)

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Branch: nxd/s-1 (not started)") {
		t.Fatalf("review must name the branch it would act on and say the story never started, got:\n%s", out)
	}
}
