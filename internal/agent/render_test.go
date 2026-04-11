package agent

import (
	"strings"
	"testing"
)

func TestRenderTemplate_BasicSubstitution(t *testing.T) {
	tmpl := "Hello {{.StoryTitle}}, you are working on {{.StoryID}}"
	ctx := TemplateContext{StoryID: "s-001", StoryTitle: "Add login page"}
	result := RenderTemplate(tmpl, ctx)
	if !strings.Contains(result, "Add login page") {
		t.Errorf("template should substitute StoryTitle, got: %s", result)
	}
	if !strings.Contains(result, "s-001") {
		t.Errorf("template should substitute StoryID, got: %s", result)
	}
}

func TestRenderTemplate_Conditional(t *testing.T) {
	tmpl := `{{if .IsRetry}}RETRY attempt {{.RetryNumber}}{{else}}First attempt{{end}}`

	first := RenderTemplate(tmpl, TemplateContext{IsRetry: false})
	if first != "First attempt" {
		t.Errorf("first attempt should render 'First attempt', got: %q", first)
	}

	retry := RenderTemplate(tmpl, TemplateContext{IsRetry: true, RetryNumber: 3})
	if retry != "RETRY attempt 3" {
		t.Errorf("retry should render 'RETRY attempt 3', got: %q", retry)
	}
}

func TestRenderTemplate_InvalidTemplate(t *testing.T) {
	tmpl := "Hello {{.Invalid"
	result := RenderTemplate(tmpl, TemplateContext{})
	if result != tmpl {
		t.Errorf("invalid template should return raw string, got: %q", result)
	}
}

func TestRenderTemplate_Range(t *testing.T) {
	tmpl := `{{range .PriorAttempts}}Attempt {{.Number}}: {{.Outcome}}
{{end}}`
	ctx := TemplateContext{
		PriorAttempts: []AttemptSummary{
			{Number: 1, Outcome: "qa_failed"},
			{Number: 2, Outcome: "review_failed"},
		},
	}
	result := RenderTemplate(tmpl, ctx)
	if !strings.Contains(result, "Attempt 1: qa_failed") {
		t.Errorf("should contain attempt 1, got: %s", result)
	}
	if !strings.Contains(result, "Attempt 2: review_failed") {
		t.Errorf("should contain attempt 2, got: %s", result)
	}
}

func TestRenderGoalWithAttempts_FirstAttempt(t *testing.T) {
	ctx := TemplateContext{
		StoryID:            "s-001",
		StoryTitle:         "Add login",
		StoryDescription:   "Create login page",
		AcceptanceCriteria: "- User can log in",
		IsRetry:            false,
	}
	result := RenderGoalWithAttempts(ctx)
	if !strings.Contains(result, "s-001") {
		t.Error("goal should contain story ID")
	}
	if strings.Contains(result, "Prior Attempts") {
		t.Error("first attempt should NOT contain prior attempts section")
	}
}

func TestRenderGoalWithAttempts_WithRetries(t *testing.T) {
	ctx := TemplateContext{
		StoryID:            "s-002",
		StoryTitle:         "Fix bug",
		StoryDescription:   "Fix the null pointer",
		AcceptanceCriteria: "- No crash",
		IsRetry:            true,
		RetryNumber:        3,
		PriorAttempts: []AttemptSummary{
			{Number: 1, Role: "junior", Outcome: "qa_failed", Error: "test failed: TestLogin"},
			{Number: 2, Role: "junior", Outcome: "review_failed", Error: "missing error handling"},
		},
	}
	result := RenderGoalWithAttempts(ctx)
	if !strings.Contains(result, "Prior Attempts") {
		t.Error("retry should contain prior attempts section")
	}
	if !strings.Contains(result, "TestLogin") {
		t.Error("should include error from attempt 1")
	}
	if !strings.Contains(result, "missing error handling") {
		t.Error("should include error from attempt 2")
	}
	if !strings.Contains(result, "DIFFERENT approach") {
		t.Error("should instruct agent to try a different approach")
	}
}

func TestRenderAttemptHistory_Empty(t *testing.T) {
	result := renderAttemptHistory(nil)
	// Empty or no-op is fine
	if strings.Contains(result, "Prior Attempts") {
		t.Error("nil attempts should not produce prior attempts section")
	}
}
