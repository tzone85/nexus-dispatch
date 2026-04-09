package agent

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestRoleInvestigator_Exists(t *testing.T) {
	if RoleInvestigator != "investigator" {
		t.Errorf("RoleInvestigator = %q, want %q", RoleInvestigator, "investigator")
	}
}

func TestRoleInvestigator_ExecutionMode(t *testing.T) {
	if RoleInvestigator.ExecutionMode() != ExecHybrid {
		t.Errorf("ExecutionMode = %q, want ExecHybrid", RoleInvestigator.ExecutionMode())
	}
}

func TestRoleInvestigator_ModelConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	mc := RoleInvestigator.ModelConfig(cfg.Models)
	if mc.Model == "" {
		t.Error("expected non-empty model for Investigator")
	}
}

func TestInvestigatorTools_Definitions(t *testing.T) {
	tools := InvestigatorTools()
	if len(tools) < 3 {
		t.Fatalf("expected at least 3 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("tool %q invalid parameters: %v", tool.Name, err)
		}
	}
	for _, name := range []string{"read_file", "run_command", "submit_report"} {
		if !names[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestInvestigatorSystemPrompt_NonEmpty(t *testing.T) {
	prompt := InvestigatorSystemPrompt()
	if len(prompt) < 100 {
		t.Errorf("prompt too short (%d chars)", len(prompt))
	}
}
