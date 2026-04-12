package agent

import (
	"strings"
	"testing"
)

func TestSetPluginState(t *testing.T) {
	// Save and restore original state.
	pluginMu.Lock()
	origPlaybooks := pluginPlaybooks
	origPrompts := pluginPromptOverrides
	pluginMu.Unlock()
	t.Cleanup(func() {
		SetPluginState(origPlaybooks, origPrompts)
	})

	playbooks := []PluginPlaybookEntry{
		{Content: "Custom playbook content", InjectWhen: "always", Roles: []string{"senior"}},
	}
	prompts := map[string]string{
		"senior": "Custom senior prompt for {repo_path}",
	}

	SetPluginState(playbooks, prompts)

	pluginMu.RLock()
	if len(pluginPlaybooks) != 1 {
		t.Errorf("expected 1 playbook, got %d", len(pluginPlaybooks))
	}
	if pluginPromptOverrides["senior"] != "Custom senior prompt for {repo_path}" {
		t.Error("expected prompt override to be stored")
	}
	pluginMu.RUnlock()
}

func TestSystemPrompt_PluginOverride(t *testing.T) {
	pluginMu.Lock()
	origPlaybooks := pluginPlaybooks
	origPrompts := pluginPromptOverrides
	pluginMu.Unlock()
	t.Cleanup(func() {
		SetPluginState(origPlaybooks, origPrompts)
	})

	SetPluginState(nil, map[string]string{
		"senior": "CUSTOM OVERRIDE for {repo_path}",
	})

	prompt := SystemPrompt(RoleSenior, PromptContext{RepoPath: "/test/repo"})
	if !strings.Contains(prompt, "CUSTOM OVERRIDE for /test/repo") {
		t.Errorf("expected plugin override with substituted repo path, got:\n%s", prompt)
	}
}

func TestSystemPrompt_PluginPlaybook_Always(t *testing.T) {
	pluginMu.Lock()
	origPlaybooks := pluginPlaybooks
	origPrompts := pluginPromptOverrides
	pluginMu.Unlock()
	t.Cleanup(func() {
		SetPluginState(origPlaybooks, origPrompts)
	})

	SetPluginState([]PluginPlaybookEntry{
		{Content: "ALWAYS INJECTED PLAYBOOK", InjectWhen: "always"},
	}, nil)

	prompt := SystemPrompt(RoleJunior, PromptContext{})
	if !strings.Contains(prompt, "ALWAYS INJECTED PLAYBOOK") {
		t.Error("expected always-injected playbook in prompt")
	}
}

func TestSystemPrompt_PluginPlaybook_RoleFiltered(t *testing.T) {
	pluginMu.Lock()
	origPlaybooks := pluginPlaybooks
	origPrompts := pluginPromptOverrides
	pluginMu.Unlock()
	t.Cleanup(func() {
		SetPluginState(origPlaybooks, origPrompts)
	})

	SetPluginState([]PluginPlaybookEntry{
		{Content: "SENIOR ONLY", InjectWhen: "always", Roles: []string{"senior"}},
	}, nil)

	// Junior should NOT get the senior-only playbook.
	juniorPrompt := SystemPrompt(RoleJunior, PromptContext{})
	if strings.Contains(juniorPrompt, "SENIOR ONLY") {
		t.Error("junior prompt should not contain senior-only playbook")
	}

	// Senior SHOULD get it.
	seniorPrompt := SystemPrompt(RoleSenior, PromptContext{})
	if !strings.Contains(seniorPrompt, "SENIOR ONLY") {
		t.Error("senior prompt should contain senior-only playbook")
	}
}

func TestSystemPrompt_PluginPlaybook_ExistingOnly(t *testing.T) {
	pluginMu.Lock()
	origPlaybooks := pluginPlaybooks
	origPrompts := pluginPromptOverrides
	pluginMu.Unlock()
	t.Cleanup(func() {
		SetPluginState(origPlaybooks, origPrompts)
	})

	SetPluginState([]PluginPlaybookEntry{
		{Content: "EXISTING CODEBASE", InjectWhen: "existing"},
	}, nil)

	// Not existing → should NOT inject.
	prompt := SystemPrompt(RoleSenior, PromptContext{IsExistingCodebase: false})
	if strings.Contains(prompt, "EXISTING CODEBASE") {
		t.Error("should not inject 'existing' playbook when not existing codebase")
	}

	// Existing → should inject.
	prompt2 := SystemPrompt(RoleSenior, PromptContext{IsExistingCodebase: true})
	if !strings.Contains(prompt2, "EXISTING CODEBASE") {
		t.Error("should inject 'existing' playbook when existing codebase")
	}
}

func TestGoalPrompt_ExistingCodebase(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Login",
		StoryDescription: "Add login", AcceptanceCriteria: "works",
		IsExistingCodebase: true,
	})
	if !strings.Contains(goal, "MANDATORY WORKFLOW FOR EXISTING CODEBASE") {
		t.Error("expected existing codebase workflow in goal")
	}
}

func TestGoalPrompt_BugFix(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Fix crash",
		StoryDescription: "Fix null pointer", AcceptanceCriteria: "no crash",
		IsBugFix: true,
	})
	if !strings.Contains(goal, "MANDATORY BUG FIX WORKFLOW") {
		t.Error("expected bug fix workflow in goal")
	}
}

func TestGoalPrompt_Infrastructure(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Fix Docker",
		StoryDescription: "Fix container", AcceptanceCriteria: "runs",
		IsInfrastructure: true,
	})
	if !strings.Contains(goal, "MANDATORY INFRASTRUCTURE WORKFLOW") {
		t.Error("expected infra workflow in goal")
	}
}

func TestGoalPrompt_WithFeedback(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Task",
		StoryDescription: "Do thing", AcceptanceCriteria: "done",
		ReviewFeedback: "Missing error handling",
	})
	if !strings.Contains(goal, "Previous Review Feedback") {
		t.Error("expected review feedback section")
	}
	if !strings.Contains(goal, "Missing error handling") {
		t.Error("expected feedback content")
	}
}

func TestGoalPrompt_WithPriorWork(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Task",
		StoryDescription: "Do thing", AcceptanceCriteria: "done",
		PriorWorkContext: "## Prior Work\n- Auth module built",
	})
	if !strings.Contains(goal, "Prior Work") {
		t.Error("expected prior work context in goal")
	}
}

func TestGoalPrompt_WithWaveBrief(t *testing.T) {
	goal := GoalPrompt(RoleSenior, PromptContext{
		StoryID: "s-001", StoryTitle: "Task",
		StoryDescription: "Do thing", AcceptanceCriteria: "done",
		WaveBrief: "## Wave Brief\n- s-002 is building the API",
	})
	if !strings.Contains(goal, "Wave Brief") {
		t.Error("expected wave brief in goal")
	}
}
