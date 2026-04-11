package engine

import "fmt"

// RecoveryActionType describes the corrective action to take for a detected
// consistency issue.
type RecoveryActionType string

const (
	ActionResetToDraft        RecoveryActionType = "reset_to_draft"
	ActionResumeMerge         RecoveryActionType = "resume_merge"
	ActionCreatePRAndMerge    RecoveryActionType = "create_pr_and_merge"
	ActionResetToReviewPassed RecoveryActionType = "reset_to_review_passed"
)

// RecoveryIssue captures a single consistency problem and the recommended
// corrective action.
type RecoveryIssue struct {
	StoryID  string
	Status   string
	Action   RecoveryActionType
	Detail   string
	PRNumber int
}

// RecoveryStory is a representation of story state for consistency checks,
// decoupled from I/O for testability.
type RecoveryStory struct {
	ID           string
	Status       string
	HasTmux      bool
	HasWorktree  bool
	BranchPushed bool
	PRNumber     int
}

// CheckConsistency inspects stories for inconsistencies that indicate a
// crash or interruption. Returns recovery actions for each issue found.
// The optional Checkpoint pointer is accepted for future extension but
// is not used in the current implementation.
func CheckConsistency(stories []RecoveryStory, cp *Checkpoint) []RecoveryIssue {
	var issues []RecoveryIssue

	for _, s := range stories {
		switch s.Status {
		case "in_progress":
			if !s.HasTmux && !s.HasWorktree {
				issues = append(issues, RecoveryIssue{
					StoryID: s.ID,
					Status:  s.Status,
					Action:  ActionResetToDraft,
					Detail:  "no tmux session and no worktree — work lost, resetting to draft",
				})
			}
			// HasTmux=false, HasWorktree=true -> existing orphan recovery handles this

		case "merging":
			if s.BranchPushed && s.PRNumber > 0 {
				issues = append(issues, RecoveryIssue{
					StoryID:  s.ID,
					Status:   s.Status,
					Action:   ActionResumeMerge,
					Detail:   fmt.Sprintf("PR #%d exists, resuming merge", s.PRNumber),
					PRNumber: s.PRNumber,
				})
			} else if s.BranchPushed {
				issues = append(issues, RecoveryIssue{
					StoryID: s.ID,
					Status:  s.Status,
					Action:  ActionCreatePRAndMerge,
					Detail:  "branch pushed but no PR — creating PR and merging",
				})
			} else {
				issues = append(issues, RecoveryIssue{
					StoryID: s.ID,
					Status:  s.Status,
					Action:  ActionResetToReviewPassed,
					Detail:  "branch not pushed — resetting to re-enter merge pipeline",
				})
			}
		}
	}

	return issues
}
