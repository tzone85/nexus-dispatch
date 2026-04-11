package engine

import "testing"

func TestCheckConsistency_LostStory(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s1", Status: "in_progress", HasTmux: false, HasWorktree: false},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	if issues[0].Action != ActionResetToDraft {
		t.Errorf("action = %q, want %q", issues[0].Action, ActionResetToDraft)
	}
}

func TestCheckConsistency_OrphanStory(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s2", Status: "in_progress", HasTmux: false, HasWorktree: true},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 0 {
		t.Fatalf("issues = %d, want 0 (orphan recovery handles this)", len(issues))
	}
}

func TestCheckConsistency_MergingWithPR(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s3", Status: "merging", PRNumber: 42, BranchPushed: true},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	if issues[0].Action != ActionResumeMerge {
		t.Errorf("action = %q, want %q", issues[0].Action, ActionResumeMerge)
	}
	if issues[0].PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", issues[0].PRNumber)
	}
}

func TestCheckConsistency_MergingBranchPushedNoPR(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s4", Status: "merging", PRNumber: 0, BranchPushed: true},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	if issues[0].Action != ActionCreatePRAndMerge {
		t.Errorf("action = %q, want %q", issues[0].Action, ActionCreatePRAndMerge)
	}
}

func TestCheckConsistency_MergingNotPushed(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s5", Status: "merging", PRNumber: 0, BranchPushed: false},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	if issues[0].Action != ActionResetToReviewPassed {
		t.Errorf("action = %q, want %q", issues[0].Action, ActionResetToReviewPassed)
	}
}

func TestCheckConsistency_HealthyStory(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s6", Status: "merged"},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 0 {
		t.Fatalf("issues = %d, want 0", len(issues))
	}
}

func TestCheckConsistency_MultipleIssues(t *testing.T) {
	stories := []RecoveryStory{
		{ID: "s1", Status: "in_progress", HasTmux: false, HasWorktree: false},
		{ID: "s2", Status: "merged"},
		{ID: "s3", Status: "merging", PRNumber: 10, BranchPushed: true},
	}
	issues := CheckConsistency(stories, nil)
	if len(issues) != 2 {
		t.Fatalf("issues = %d, want 2", len(issues))
	}
}
