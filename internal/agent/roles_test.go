package agent_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestRouteByComplexity(t *testing.T) {
	routing := config.RoutingConfig{
		JuniorMaxComplexity:       3,
		IntermediateMaxComplexity: 5,
	}

	tests := []struct {
		complexity int
		expected   agent.Role
	}{
		{1, agent.RoleJunior},
		{2, agent.RoleJunior},
		{3, agent.RoleJunior},
		{4, agent.RoleIntermediate},
		{5, agent.RoleIntermediate},
		{6, agent.RoleSenior},
		{8, agent.RoleSenior},
		{13, agent.RoleSenior},
	}

	for _, tt := range tests {
		role := agent.RouteByComplexity(tt.complexity, routing)
		if role != tt.expected {
			t.Errorf("complexity %d: expected %s, got %s", tt.complexity, tt.expected, role)
		}
	}
}

func TestRole_ExecutionMode(t *testing.T) {
	tests := []struct {
		role     agent.Role
		expected agent.ExecutionMode
	}{
		{agent.RoleTechLead, agent.ExecAPI},
		{agent.RoleSupervisor, agent.ExecAPI},
		{agent.RoleSenior, agent.ExecAPI},
		{agent.RoleIntermediate, agent.ExecCLI},
		{agent.RoleJunior, agent.ExecCLI},
		{agent.RoleQA, agent.ExecHybrid},
	}

	for _, tt := range tests {
		if tt.role.ExecutionMode() != tt.expected {
			t.Errorf("role %s: expected %s, got %s", tt.role, tt.expected, tt.role.ExecutionMode())
		}
	}
}

func TestRole_ModelConfig(t *testing.T) {
	models := config.ModelsConfig{
		TechLead:     config.ModelConfig{Model: "opus"},
		Senior:       config.ModelConfig{Model: "sonnet"},
		Intermediate: config.ModelConfig{Model: "haiku"},
		Junior:       config.ModelConfig{Model: "mini"},
		QA:           config.ModelConfig{Model: "sonnet-qa"},
		Supervisor:   config.ModelConfig{Model: "sonnet-super"},
	}

	if agent.RoleTechLead.ModelConfig(models).Model != "opus" {
		t.Fatal("tech lead should use opus")
	}
	if agent.RoleJunior.ModelConfig(models).Model != "mini" {
		t.Fatal("junior should use mini")
	}
}

func TestRole_String(t *testing.T) {
	if agent.RoleTechLead.String() != "tech_lead" {
		t.Fatalf("expected 'tech_lead', got %s", agent.RoleTechLead.String())
	}
}
