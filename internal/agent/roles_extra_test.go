package agent

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// TestRole_ModelConfig_AllRoles exercises every branch of
// Role.ModelConfig — the existing test only covers Junior/Senior/etc.
// A future role addition without updating ModelConfig would fall
// through to RoleJunior. This test pins the exact mapping so any
// drift fails loudly.
func TestRole_ModelConfig_AllRoles(t *testing.T) {
	models := config.ModelsConfig{
		TechLead:     config.ModelConfig{Model: "tech-lead-model"},
		Senior:       config.ModelConfig{Model: "senior-model"},
		Intermediate: config.ModelConfig{Model: "intermediate-model"},
		Junior:       config.ModelConfig{Model: "junior-model"},
		QA:           config.ModelConfig{Model: "qa-model"},
		Supervisor:   config.ModelConfig{Model: "supervisor-model"},
		Manager:      config.ModelConfig{Model: "manager-model"},
		Investigator: config.ModelConfig{Model: "investigator-model"},
	}
	cases := map[Role]string{
		RoleTechLead:     "tech-lead-model",
		RoleSenior:       "senior-model",
		RoleIntermediate: "intermediate-model",
		RoleJunior:       "junior-model",
		RoleQA:           "qa-model",
		RoleSupervisor:   "supervisor-model",
		RoleManager:      "manager-model",
		RoleInvestigator: "investigator-model",
	}
	for role, want := range cases {
		t.Run(string(role), func(t *testing.T) {
			got := role.ModelConfig(models)
			if got.Model != want {
				t.Errorf("ModelConfig(%s) = %q, want %q", role, got.Model, want)
			}
		})
	}
}

// TestRole_ModelConfig_UnknownFallsBackToJunior covers the default
// branch — a role string that doesn't match any case must fall back
// to RoleJunior to avoid a panic / nil deref in callers.
func TestRole_ModelConfig_UnknownFallsBackToJunior(t *testing.T) {
	models := config.ModelsConfig{Junior: config.ModelConfig{Model: "junior-fallback"}}
	got := Role("unknown-role").ModelConfig(models)
	if got.Model != "junior-fallback" {
		t.Errorf("unknown role should fall back to Junior; got %q", got.Model)
	}
}
