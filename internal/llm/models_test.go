package llm_test

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestRecommendedModels_AllRolesCovered(t *testing.T) {
	models := llm.RecommendedModels()
	if len(models) != 8 {
		t.Fatalf("expected 8 recommended models, got %d", len(models))
	}

	// First 4 models must be Gemma 4 family
	for i := 0; i < 4; i++ {
		if !strings.HasPrefix(models[i].Name, "gemma4:") {
			t.Errorf("model[%d] expected gemma4 prefix, got %q", i, models[i].Name)
		}
	}

	// Every model must have required fields populated
	for _, m := range models {
		if m.Name == "" {
			t.Errorf("model for role %q has empty name", m.Role)
		}
		if m.Parameters == "" {
			t.Errorf("model for role %q has empty parameters", m.Role)
		}
		if m.MinRAMGB <= 0 {
			t.Errorf("model for role %q has invalid MinRAMGB %d", m.Role, m.MinRAMGB)
		}
		if m.Description == "" {
			t.Errorf("model for role %q has empty description", m.Role)
		}
		if m.Role == "" {
			t.Errorf("model %q has empty role", m.Name)
		}
	}
}

func TestModelForRole_KnownRoles(t *testing.T) {
	roles := []string{
		"tech_lead",
		"senior",
		"intermediate",
		"junior",
		"qa",
		"supervisor",
	}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			got := llm.ModelForRole(role)
			if got != "gemma4:26b" {
				t.Errorf("ModelForRole(%q) = %q, want %q", role, got, "gemma4:26b")
			}
		})
	}
}

func TestModelForRole_UnknownRole(t *testing.T) {
	got := llm.ModelForRole("nonexistent_role")
	if got != "gemma4:26b" {
		t.Errorf("ModelForRole('nonexistent_role') = %q, want %q", got, "gemma4:26b")
	}
}

func TestModelForRole_EmptyRole(t *testing.T) {
	got := llm.ModelForRole("")
	if got != "gemma4:26b" {
		t.Errorf("ModelForRole('') = %q, want %q", got, "gemma4:26b")
	}
}
