package agent

import (
	"fmt"
	"strings"
)

// PromptContext holds the values substituted into system prompt templates.
type PromptContext struct {
	TeamName           string
	RepoPath           string
	TechStack          string
	StoryID            string
	StoryTitle         string
	StoryDescription   string
	AcceptanceCriteria string
	Complexity         int
	LintCommand        string
	BuildCommand       string
	TestCommand        string
	ReviewFeedback     string
	IsExistingCodebase bool
	IsBugFix           bool
	IsRefactor         bool
	IsInfrastructure   bool
	InvestigationReport string // formatted markdown, injected by planner
}

// SystemPrompt renders the system prompt for the given role, substituting
// placeholders from the provided context. It conditionally appends diagnostic
// playbooks based on the context flags (existing codebase, bug fix, etc.).
func SystemPrompt(role Role, ctx PromptContext) string {
	tmpl := promptTemplates[role]
	base := replacePlaceholders(tmpl, ctx)

	var extras []string

	if ctx.IsExistingCodebase {
		switch role {
		case RoleTechLead:
			extras = append(extras, CodebaseArchaeology)
		case RoleSenior:
			extras = append(extras, BugHuntingMethodology, LegacyCodeSurvival)
		case RoleIntermediate, RoleJunior:
			extras = append(extras, LegacyCodeSurvival)
		}
	}

	if ctx.IsBugFix && !ctx.IsExistingCodebase {
		if role == RoleSenior || role == RoleIntermediate {
			extras = append(extras, BugHuntingMethodology)
		}
	}

	if ctx.IsInfrastructure {
		extras = append(extras, InfrastructureDebugging)
	}

	if ctx.InvestigationReport != "" && role == RoleTechLead {
		extras = append(extras, ctx.InvestigationReport)
	}

	if len(extras) > 0 {
		return base + "\n\n" + strings.Join(extras, "\n\n")
	}
	return base
}

// GoalPrompt builds the task description sent to the runtime CLI for a given role and story.
func GoalPrompt(role Role, ctx PromptContext) string {
	base := fmt.Sprintf(`Implement story %s: %s

Description: %s

Acceptance Criteria:
%s

IMPORTANT INSTRUCTIONS:
- Do NOT ask questions. Do NOT brainstorm. Do NOT request clarification.
- Implement the code directly based on the description and acceptance criteria above.
- Make reasonable assumptions for any unspecified details.
- Work in the current directory. Create or modify files as needed.
- Write tests to verify your implementation.
- Commit all changes to git when done.`,
		ctx.StoryID, ctx.StoryTitle, ctx.StoryDescription, ctx.AcceptanceCriteria)

	if ctx.ReviewFeedback != "" {
		base += fmt.Sprintf(`

## Previous Review Feedback (MUST ADDRESS)
The previous implementation was rejected. Fix these issues:
%s`, ctx.ReviewFeedback)
	}

	goal := base

	if ctx.IsExistingCodebase {
		goal += "\n\nMANDATORY WORKFLOW FOR EXISTING CODEBASE:\n1. ORIENT: ls -la, read README.md, read CLAUDE.md\n2. MAP: find source files relevant to this story\n3. HISTORY: git log --oneline -15\n4. BASELINE: run existing test suite, record what passes\n5. SEARCH: grep for functions/types related to this story\n6. READ: open and read the relevant files\n7. THEN implement, matching existing code style"
	}
	if ctx.IsBugFix {
		goal += "\n\nMANDATORY BUG FIX WORKFLOW:\n1. REPRODUCE: write a failing test\n2. ISOLATE: read stack trace, add logging\n3. ROOT CAUSE: understand WHY it's broken\n4. FIX: minimal change only\n5. VERIFY: test passes, full suite passes, no regressions"
	}
	if ctx.IsInfrastructure {
		goal += "\n\nMANDATORY INFRASTRUCTURE WORKFLOW:\n1. Check services: docker ps -a, lsof for LISTEN\n2. Check logs: docker logs --tail 50, journalctl\n3. Check config: env vars, .env, docker-compose.yml\n4. Check resources: df -h, memory\n5. Fix and verify with health checks"
	}

	return goal
}

var promptTemplates = map[Role]string{
	RoleTechLead: `You are the Tech Lead of NXD, an AI development team orchestrator.

Your Responsibilities:
1. Receive requirements and decompose them into atomic, testable stories
2. Identify dependencies between stories
3. Assign complexity scores (Fibonacci: 1, 2, 3, 5, 8, 13)
4. Ensure each story has clear acceptance criteria
5. Output stories as structured JSON

Current Repository: {repo_path}
Tech Stack: {tech_stack}

Guidelines:
- Each story must be independently implementable
- Stories with score 1-3 should be simple enough for a junior developer
- Stories with score 4-5 need intermediate-level work
- Stories with score 6+ need senior-level architecture decisions
- Identify cross-story dependencies explicitly`,

	RoleSenior: `You are a Senior Developer on Team {team_name}.

Your assignment:
Story: {story_id} - {story_title}
Description: {story_description}
Acceptance Criteria: {acceptance_criteria}

Repository: {repo_path}
Tech Stack: {tech_stack}

Guidelines:
- You are running autonomously. Do NOT ask questions or request input.
- Create a feature branch: nxd/{story_id}
- Implement the story completely with clean, tested code
- Follow existing patterns in the codebase
- Commit your work when done`,

	RoleIntermediate: `You are an Intermediate Developer on Team {team_name}.

Your assignment:
Story: {story_id} - {story_title}
Description: {story_description}
Acceptance Criteria: {acceptance_criteria}

Repository: {repo_path}
Tech Stack: {tech_stack}

Guidelines:
- You are running autonomously. Do NOT ask questions or request input.
- Create a feature branch: nxd/{story_id}
- Implement the story completely
- Write tests for your changes
- Commit your work when done`,

	RoleJunior: `You are a Junior Developer on Team {team_name}.

Your assignment:
Story: {story_id} - {story_title}
Description: {story_description}
Acceptance Criteria: {acceptance_criteria}

Repository: {repo_path}
Tech Stack: {tech_stack}

Guidelines:
- You are running autonomously. Do NOT ask questions or request input.
- Create a feature branch: nxd/{story_id}
- Implement the story step by step
- Write tests for your changes
- Commit your work when done`,

	RoleQA: `You are the QA Agent for Team {team_name}.

Your Responsibilities:
1. Run quality checks on completed stories
2. Verify acceptance criteria are met
3. Approve or reject with clear feedback

Quality Checklist:
- Code passes linting: {lint_command}
- Build succeeds: {build_command}
- Tests pass: {test_command}
- Changes align with acceptance criteria
- No obvious security issues

On Failure: provide specific, actionable feedback
On Success: approve for PR creation`,

	RoleSupervisor: `You are the Supervisor reviewing progress for the current requirement.

Review the current state of stories and determine:
1. Are the stories progressing toward the original requirement?
2. Is any story drifting from the intended goal?
3. Should any stories be reprioritized?
4. Are there any concerns about the overall approach?

Respond with a structured assessment.`,
}

func replacePlaceholders(tmpl string, ctx PromptContext) string {
	r := strings.NewReplacer(
		"{team_name}", ctx.TeamName,
		"{repo_path}", ctx.RepoPath,
		"{tech_stack}", ctx.TechStack,
		"{story_id}", ctx.StoryID,
		"{story_title}", ctx.StoryTitle,
		"{story_description}", ctx.StoryDescription,
		"{acceptance_criteria}", ctx.AcceptanceCriteria,
		"{lint_command}", ctx.LintCommand,
		"{build_command}", ctx.BuildCommand,
		"{test_command}", ctx.TestCommand,
	)
	return r.Replace(tmpl)
}
