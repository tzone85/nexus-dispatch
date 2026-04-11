package agent

import (
	"bytes"
	"text/template"
)

// TemplateContext holds all data available to prompt templates.
type TemplateContext struct {
	// Story context
	StoryID            string
	StoryTitle         string
	StoryDescription   string
	AcceptanceCriteria string
	Complexity         int

	// Team/repo context
	TeamName  string
	RepoPath  string
	TechStack string

	// QA commands
	LintCommand  string
	BuildCommand string
	TestCommand  string

	// Prior work
	WaveContext    string           // what prior stories built
	PriorAttempts  []AttemptSummary // prior attempts for this story
	ReviewFeedback string           // feedback from failed review

	// Contextual flags
	IsExistingCodebase bool
	IsBugFix           bool
	IsInfrastructure   bool
	IsRetry            bool // true if this is not the first attempt
	RetryNumber        int  // which attempt this is (1-indexed)
}

// AttemptSummary is a simplified view of a prior attempt for prompt injection.
type AttemptSummary struct {
	Number  int
	Role    string
	Outcome string
	Error   string
}

// RenderTemplate renders a Go text/template string with the given context.
// Falls back to returning the raw template if rendering fails.
func RenderTemplate(tmpl string, ctx TemplateContext) string {
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return tmpl
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return tmpl
	}
	return buf.String()
}

// RenderGoalWithAttempts builds a goal prompt that includes prior attempt
// context, so retry agents understand what was tried before and why it failed.
func RenderGoalWithAttempts(ctx TemplateContext) string {
	// Build a PromptContext from the TemplateContext to reuse existing GoalPrompt.
	pc := PromptContext{
		TeamName:           ctx.TeamName,
		RepoPath:           ctx.RepoPath,
		TechStack:          ctx.TechStack,
		StoryID:            ctx.StoryID,
		StoryTitle:         ctx.StoryTitle,
		StoryDescription:   ctx.StoryDescription,
		AcceptanceCriteria: ctx.AcceptanceCriteria,
		LintCommand:        ctx.LintCommand,
		BuildCommand:       ctx.BuildCommand,
		TestCommand:        ctx.TestCommand,
		ReviewFeedback:     ctx.ReviewFeedback,
		IsExistingCodebase: ctx.IsExistingCodebase,
		IsBugFix:           ctx.IsBugFix,
		IsInfrastructure:   ctx.IsInfrastructure,
	}

	// Route to the appropriate role based on complexity.
	role := RoleJunior
	if ctx.Complexity >= 5 {
		role = RoleSenior
	} else if ctx.Complexity >= 3 {
		role = RoleIntermediate
	}
	base := GoalPrompt(role, pc)

	// If this is a retry with prior attempts, append attempt history.
	if ctx.IsRetry && len(ctx.PriorAttempts) > 0 {
		base += renderAttemptHistory(ctx.PriorAttempts)
	}

	return base
}

const attemptHistoryTemplate = `{{if .}}

## Prior Attempts (LEARN FROM THESE)
This story has been attempted {{len .}} time(s) before. Each attempt failed.
Study the failures below and take a DIFFERENT approach.

{{range .}}### Attempt {{.Number}} ({{.Role}})
- Outcome: {{.Outcome}}
{{if .Error}}- Error: {{.Error}}
{{end}}{{end}}
DO NOT repeat the same mistakes. If the prior approach failed, try a fundamentally different strategy.{{end}}`

func renderAttemptHistory(attempts []AttemptSummary) string {
	t, err := template.New("attempts").Parse(attemptHistoryTemplate)
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, attempts); err != nil {
		return ""
	}
	return buf.String()
}
