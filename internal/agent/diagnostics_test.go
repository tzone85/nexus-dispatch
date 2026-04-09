package agent_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
)

func TestCodebaseArchaeology_ContainsKeySections(t *testing.T) {
	playbook := agent.CodebaseArchaeology

	keywords := []string{
		"BEFORE PLANNING",
		"Investigation Report",
		"test coverage",
		"build health",
		"file ownership",
		"Inventory",
		"Architecture Map",
		"Convention Detection",
		"Health Assessment",
		"Risk Identification",
		"Story Planning Constraints",
		"Codebase Orientation",
	}
	for _, kw := range keywords {
		if !containsString(playbook, kw) {
			t.Errorf("CodebaseArchaeology missing keyword: %q", kw)
		}
	}
}

func TestCodebaseArchaeology_IsSubstantial(t *testing.T) {
	if len(agent.CodebaseArchaeology) < 200 {
		t.Fatalf("CodebaseArchaeology too short: %d chars", len(agent.CodebaseArchaeology))
	}
}

func TestBugHuntingMethodology_ContainsKeySections(t *testing.T) {
	playbook := agent.BugHuntingMethodology

	keywords := []string{
		"PHASE 1: REPRODUCE",
		"PHASE 2: ISOLATE",
		"PHASE 3: UNDERSTAND ROOT CAUSE",
		"PHASE 4: MINIMAL FIX",
		"PHASE 5: VERIFY",
		"failing test",
		"Stack Trace",
		"git bisect",
		"git blame",
		"COMMON BUG PATTERNS",
		"Nil/Null Pointer",
		"Race Condition",
		"Off-by-One",
		"Type Coercion",
		"Environment-Dependent",
		"State Mutation",
		"Error Swallowing",
		"Zero-Value Fields",
		"Resource Leaks",
	}
	for _, kw := range keywords {
		if !containsString(playbook, kw) {
			t.Errorf("BugHuntingMethodology missing keyword: %q", kw)
		}
	}
}

func TestBugHuntingMethodology_IsSubstantial(t *testing.T) {
	if len(agent.BugHuntingMethodology) < 200 {
		t.Fatalf("BugHuntingMethodology too short: %d chars", len(agent.BugHuntingMethodology))
	}
}

func TestInfrastructureDebugging_ContainsKeySections(t *testing.T) {
	playbook := agent.InfrastructureDebugging

	keywords := []string{
		"DOCKER",
		"docker ps",
		"docker logs",
		"docker inspect",
		"docker compose",
		"docker network",
		"DATABASE",
		"PostgreSQL",
		"SQLite",
		"MySQL",
		"CI/CD",
		"gh run list",
		"gh run view",
		"Dependency Drift",
		"NETWORK",
		"curl -v",
		"lsof",
		"DNS",
		"TLS",
		"ENVIRONMENT",
		"env",
		"PATH",
		"LOG ANALYSIS",
		"grep",
		"journalctl",
		"COMMON INFRASTRUCTURE FAILURES",
		"Port Conflict",
		"Permission Denied",
		"Disk Full",
		"Out of Memory",
		"Connection Timeout",
	}
	for _, kw := range keywords {
		if !containsString(playbook, kw) {
			t.Errorf("InfrastructureDebugging missing keyword: %q", kw)
		}
	}
}

func TestInfrastructureDebugging_IsSubstantial(t *testing.T) {
	if len(agent.InfrastructureDebugging) < 200 {
		t.Fatalf("InfrastructureDebugging too short: %d chars", len(agent.InfrastructureDebugging))
	}
}

func TestLegacyCodeSurvival_ContainsKeySections(t *testing.T) {
	playbook := agent.LegacyCodeSurvival

	keywords := []string{
		"5 GOLDEN RULES",
		"NEVER rewrite",
		"characterization tests",
		"small steps",
		"Commit often",
		"WORKING WITH UNKNOWN CODE",
		"Trace Entry Points",
		"Grep Before You Write",
		"Read Tests First",
		"Git Blame",
		"Follow Existing Patterns",
		"SAFE REFACTORING STEPS",
		"Extract Function",
		"Rename",
		"Remove Dead Code",
		"Add Type Annotations",
		"Add Error Handling",
		"Restructure",
		"WHAT NOT TO DO",
		"directory restructuring",
		"formatting changes",
		"premature abstractions",
		"WHEN TESTS DON'T EXIST",
		"Characterization Tests",
		"Commit Tests Separately",
	}
	for _, kw := range keywords {
		if !containsString(playbook, kw) {
			t.Errorf("LegacyCodeSurvival missing keyword: %q", kw)
		}
	}
}

func TestLegacyCodeSurvival_IsSubstantial(t *testing.T) {
	if len(agent.LegacyCodeSurvival) < 200 {
		t.Fatalf("LegacyCodeSurvival too short: %d chars", len(agent.LegacyCodeSurvival))
	}
}

func TestAllPlaybooks_NonEmpty(t *testing.T) {
	playbooks := map[string]string{
		"CodebaseArchaeology":     agent.CodebaseArchaeology,
		"BugHuntingMethodology":   agent.BugHuntingMethodology,
		"InfrastructureDebugging": agent.InfrastructureDebugging,
		"LegacyCodeSurvival":     agent.LegacyCodeSurvival,
	}
	for name, content := range playbooks {
		if content == "" {
			t.Errorf("playbook %s is empty", name)
		}
		if len(content) < 200 {
			t.Errorf("playbook %s is too short (%d chars, minimum 200)", name, len(content))
		}
	}
}

// containsString checks whether s contains substr. Extracted as a helper
// so tests read cleanly without importing strings in the test file.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
