package agent_test

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
)

func TestSystemPrompt_TechLead(t *testing.T) {
	ctx := agent.PromptContext{
		RepoPath:  "/path/to/repo",
		TechStack: "Go, PostgreSQL",
	}
	prompt := agent.SystemPrompt(agent.RoleTechLead, ctx)
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
	ctx := agent.PromptContext{
		TeamName:           "api",
		StoryID:            "s-001",
		StoryTitle:         "Add login endpoint",
		StoryDescription:   "Create POST /api/login",
		AcceptanceCriteria: "Returns JWT on valid credentials",
		RepoPath:           "/repo",
		TechStack:          "Node.js",
	}
	prompt := agent.SystemPrompt(agent.RoleJunior, ctx)
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
	ctx := agent.PromptContext{
		TeamName:     "api",
		LintCommand:  "npm run lint",
		BuildCommand: "npm run build",
		TestCommand:  "npm test",
	}
	prompt := agent.SystemPrompt(agent.RoleQA, ctx)
	if !strings.Contains(prompt, "npm run lint") {
		t.Fatal("expected lint command")
	}
	if !strings.Contains(prompt, "npm run build") {
		t.Fatal("expected build command")
	}
}

func TestSystemPrompt_AllRolesExist(t *testing.T) {
	roles := []agent.Role{
		agent.RoleTechLead, agent.RoleSenior, agent.RoleIntermediate,
		agent.RoleJunior, agent.RoleQA, agent.RoleSupervisor,
	}
	for _, role := range roles {
		prompt := agent.SystemPrompt(role, agent.PromptContext{})
		if prompt == "" {
			t.Fatalf("empty prompt for role %s", role)
		}
	}
}
