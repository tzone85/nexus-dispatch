package agent

import (
	"strings"
	"testing"
)

// The frontend design brief is injected into the goal prompt only for
// UI-facing stories (ctx.IsFrontend). It is the vxd factory's design skill:
// token-first planning, one signature element, the named anti-pattern looks,
// and a non-negotiable accessibility floor.
func TestGoalPrompt_FrontendBriefInjectedWhenFlagSet(t *testing.T) {
	ctx := PromptContext{
		StoryID:            "s-1",
		StoryTitle:         "Build the landing page",
		StoryDescription:   "Marketing page for the product",
		AcceptanceCriteria: "- page renders",
		IsFrontend:         true,
	}
	got := GoalPrompt(RoleSenior, ctx)

	for _, want := range []string{
		"FRONTEND DESIGN",        // section header
		"Signature",              // one memorable element
		"token",                  // token-first plan before code
		"prefers-reduced-motion", // quality floor
		"focus",                  // visible keyboard focus
		"Inter",                  // named anti-pattern font
		"purple",                 // named anti-pattern palette
		"#F4F1EA",                // named second-generation cliché
		"WCAG",                   // contrast floor
		"Submit",                 // copy rule: never label a button Submit
	} {
		if !strings.Contains(got, want) {
			t.Errorf("frontend brief missing %q", want)
		}
	}
}

func TestGoalPrompt_FrontendBriefAbsentForBackendStories(t *testing.T) {
	ctx := PromptContext{
		StoryID:            "s-2",
		StoryTitle:         "Create REST API endpoints",
		StoryDescription:   "Express routes",
		AcceptanceCriteria: "- routes tested",
		IsFrontend:         false,
	}
	got := GoalPrompt(RoleSenior, ctx)
	if strings.Contains(got, "FRONTEND DESIGN") {
		t.Error("backend story must not carry the frontend design brief")
	}
}

// Retry dispatches go through RenderGoalWithAttempts — the brief must survive
// the retry path too, or the second attempt regresses to default design.
func TestRenderGoalWithAttempts_CarriesFrontendBrief(t *testing.T) {
	ctx := TemplateContext{
		StoryID:            "s-1",
		StoryTitle:         "Build the landing page",
		StoryDescription:   "Marketing page",
		AcceptanceCriteria: "- page renders",
		IsFrontend:         true,
		IsRetry:            true,
		RetryNumber:        2,
		ReviewFeedback:     "colors are generic",
		PriorAttempts:      []AttemptSummary{{Number: 1, Role: "senior", Outcome: "review_failed"}},
	}
	got := RenderGoalWithAttempts(ctx)
	if !strings.Contains(got, "FRONTEND DESIGN") {
		t.Error("retry path must carry the frontend design brief")
	}
}

// The brief itself must stay within a sane token budget — it rides on every
// UI story dispatch. ~6k chars ≈ 1.5k tokens is the ceiling.
func TestFrontendDesignBrief_SizeBudget(t *testing.T) {
	if n := len(FrontendDesignBrief); n > 6000 {
		t.Errorf("FrontendDesignBrief is %d chars — trim it below 6000 (prompt budget)", n)
	}
	if n := len(FrontendDesignBrief); n < 1500 {
		t.Errorf("FrontendDesignBrief is %d chars — suspiciously small, did the content get lost?", n)
	}
}
