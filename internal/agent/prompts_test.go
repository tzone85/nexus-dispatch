package agent

import (
	"strings"
	"testing"
)

func TestSystemPrompt_TechLead(t *testing.T) {
	ctx := PromptContext{
		RepoPath:  "/path/to/repo",
		TechStack: "Go, PostgreSQL",
	}
	prompt := SystemPrompt(RoleTechLead, ctx)
	if !strings.Contains(prompt, "/path/to/repo") {
		t.Fatal("expected repo path in prompt")
	}
	if !strings.Contains(prompt, "Go, PostgreSQL") {
		t.Fatal("expected tech stack in prompt")
	}
	if !strings.Contains(prompt, "Fibonacci") {
		t.Fatal("expected complexity scoring guidance")
	}
}

func TestSystemPrompt_Junior(t *testing.T) {
	ctx := PromptContext{
		TeamName:           "api",
		StoryID:            "s-001",
		StoryTitle:         "Add login endpoint",
		StoryDescription:   "Create POST /api/login",
		AcceptanceCriteria: "Returns JWT on valid credentials",
		RepoPath:           "/repo",
		TechStack:          "Node.js",
	}
	prompt := SystemPrompt(RoleJunior, ctx)
	if !strings.Contains(prompt, "s-001") {
		t.Fatal("expected story ID")
	}
	if !strings.Contains(prompt, "Add login endpoint") {
		t.Fatal("expected story title")
	}
	if !strings.Contains(prompt, "Team api") {
		t.Fatal("expected team name")
	}
}

func TestSystemPrompt_QA(t *testing.T) {
	ctx := PromptContext{
		TeamName:     "api",
		LintCommand:  "npm run lint",
		BuildCommand: "npm run build",
		TestCommand:  "npm test",
	}
	prompt := SystemPrompt(RoleQA, ctx)
	if !strings.Contains(prompt, "npm run lint") {
		t.Fatal("expected lint command")
	}
	if !strings.Contains(prompt, "npm run build") {
		t.Fatal("expected build command")
	}
}

func TestSystemPrompt_AllRolesExist(t *testing.T) {
	roles := []Role{
		RoleTechLead, RoleSenior, RoleIntermediate,
		RoleJunior, RoleQA, RoleSupervisor,
	}
	for _, role := range roles {
		prompt := SystemPrompt(role, PromptContext{})
		if prompt == "" {
			t.Fatalf("empty prompt for role %s", role)
		}
	}
}

func TestSystemPrompt_ExistingCodebase_TechLead(t *testing.T) {
	ctx := PromptContext{IsExistingCodebase: true, TechStack: "go (go)", RepoPath: "/tmp/test"}
	prompt := SystemPrompt(RoleTechLead, ctx)
	if !strings.Contains(prompt, "Investigation Report") || !strings.Contains(prompt, "BEFORE PLANNING") {
		t.Error("expected CodebaseArchaeology in TechLead prompt for existing codebase")
	}
}

func TestSystemPrompt_ExistingCodebase_Senior(t *testing.T) {
	ctx := PromptContext{IsExistingCodebase: true, TechStack: "go (go)"}
	prompt := SystemPrompt(RoleSenior, ctx)
	if !strings.Contains(prompt, "REPRODUCE") {
		t.Error("expected BugHuntingMethodology")
	}
	if !strings.Contains(prompt, "NEVER rewrite") {
		t.Error("expected LegacyCodeSurvival")
	}
}

func TestSystemPrompt_BugFix_Intermediate(t *testing.T) {
	ctx := PromptContext{IsBugFix: true, TechStack: "go (go)"}
	prompt := SystemPrompt(RoleIntermediate, ctx)
	if !strings.Contains(prompt, "REPRODUCE") {
		t.Error("expected BugHuntingMethodology for bug fix")
	}
}

func TestSystemPrompt_Infrastructure_Junior(t *testing.T) {
	ctx := PromptContext{IsInfrastructure: true, TechStack: "go (go)"}
	prompt := SystemPrompt(RoleJunior, ctx)
	if !strings.Contains(prompt, "Docker") || !strings.Contains(prompt, "docker") {
		t.Error("expected InfrastructureDebugging")
	}
}

func TestSystemPrompt_Greenfield_NoPlaybooks(t *testing.T) {
	ctx := PromptContext{TechStack: "go (go)"}
	prompt := SystemPrompt(RoleSenior, ctx)
	if strings.Contains(prompt, "REPRODUCE") {
		t.Error("BugHuntingMethodology should NOT be in greenfield prompt")
	}
	if strings.Contains(prompt, "NEVER rewrite") {
		t.Error("LegacyCodeSurvival should NOT be in greenfield prompt")
	}
}

func TestGoalPrompt_BugFix_HasWorkflow(t *testing.T) {
	ctx := PromptContext{IsBugFix: true, StoryTitle: "Fix auth bug", StoryDescription: "JWT tokens expire early"}
	goal := GoalPrompt(RoleSenior, ctx)
	if !strings.Contains(goal, "REPRODUCE") {
		t.Error("expected bug fix workflow in goal prompt")
	}
}

func TestGoalPrompt_Existing_HasOrientWorkflow(t *testing.T) {
	ctx := PromptContext{IsExistingCodebase: true, StoryTitle: "Add feature", StoryDescription: "Add endpoint"}
	goal := GoalPrompt(RoleIntermediate, ctx)
	if !strings.Contains(goal, "ORIENT") {
		t.Error("expected orientation workflow in goal prompt")
	}
}

func TestGoalPrompt_Greenfield_NoWorkflows(t *testing.T) {
	ctx := PromptContext{StoryTitle: "Build API", StoryDescription: "Create REST API"}
	goal := GoalPrompt(RoleSenior, ctx)
	if strings.Contains(goal, "ORIENT") || strings.Contains(goal, "REPRODUCE") {
		t.Error("no workflows should be added for greenfield")
	}
}
