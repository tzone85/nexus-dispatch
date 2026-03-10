package llm_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestRecommendedModels_AllRolesCovered(t *testing.T) {
	expectedRoles := map[string]bool{
		"tech_lead":    false,
		"senior":       false,
		"intermediate": false,
		"junior":       false,
		"qa":           false,
		"supervisor":   false,
	}

	models := llm.RecommendedModels()
	if len(models) != 6 {
		t.Fatalf("expected 6 recommended models, got %d", len(models))
	}

	for _, m := range models {
		if _, ok := expectedRoles[m.Role]; !ok {
			t.Errorf("unexpected role %q in recommended models", m.Role)
		}
		expectedRoles[m.Role] = true

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
	}

	for role, found := range expectedRoles {
		if !found {
			t.Errorf("role %q not covered by recommended models", role)
		}
	}
}

func TestModelForRole_KnownRoles(t *testing.T) {
	tests := []struct {
		role     string
		expected string
	}{
		{role: "tech_lead", expected: "deepseek-coder-v2:latest"},
		{role: "senior", expected: "qwen2.5-coder:32b"},
		{role: "intermediate", expected: "qwen2.5-coder:14b"},
		{role: "junior", expected: "qwen2.5-coder:7b"},
		{role: "qa", expected: "qwen2.5-coder:14b"},
		{role: "supervisor", expected: "deepseek-coder-v2:latest"},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := llm.ModelForRole(tt.role)
			if got != tt.expected {
				t.Errorf("ModelForRole(%q) = %q, want %q", tt.role, got, tt.expected)
			}
		})
	}
}

func TestModelForRole_UnknownRole(t *testing.T) {
	got := llm.ModelForRole("nonexistent_role")
	if got != "" {
		t.Errorf("ModelForRole('nonexistent_role') = %q, want empty string", got)
	}
}

func TestModelForRole_EmptyRole(t *testing.T) {
	got := llm.ModelForRole("")
	if got != "" {
		t.Errorf("ModelForRole('') = %q, want empty string", got)
	}
}
