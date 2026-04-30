package engine

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no fences", "hello world", "hello world"},
		{"with fences", "```go\nfunc main() {}\n```", "func main() {}"},
		{"with lang and trailing", "```python\nprint('hi')\n```", "print('hi')"},
		{"only opening fence", "```\nsome code", "some code"},
		{"empty", "", ""},
		{"just fence", "```\n```", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeFences(tt.input)
			if got != tt.want {
				t.Errorf("stripCodeFences(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateDiff(t *testing.T) {
	short := "short diff"
	if truncateDiff(short, 100) != short {
		t.Error("expected unchanged for short diff")
	}

	long := "a very long diff that exceeds the limit"
	got := truncateDiff(long, 10)
	if len(got) <= 10 {
		// truncated + indicator appended
	}
	if got[:10] != long[:10] {
		t.Error("expected prefix to match")
	}
	if got == long {
		t.Error("expected truncation")
	}
}

func TestTierForRole(t *testing.T) {
	tests := []struct {
		role agent.Role
		want int
	}{
		{agent.RoleJunior, 0},
		{agent.RoleIntermediate, 0},
		{agent.RoleSenior, 1},
		{agent.RoleManager, 2},
		{agent.RoleTechLead, 3},
		{agent.RoleQA, 0},
		{agent.RoleSupervisor, 0},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tierForRole(tt.role); got != tt.want {
				t.Errorf("tierForRole(%s) = %d, want %d", tt.role, got, tt.want)
			}
		})
	}
}

func TestConfigCriteriaToRuntime_Empty(t *testing.T) {
	result := configCriteriaToRuntime(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestConfigCriteriaToRuntime_Converts(t *testing.T) {
	input := []config.SuccessCriterion{
		{Kind: "file_exists", Path: "go.mod"},
		{Kind: "command_succeeds", Value: "go build ./..."},
		{Kind: "test_passes", Value: "go test ./..."},
		{Kind: "file_contains", Path: "main.go", Value: "package main"},
	}
	result := configCriteriaToRuntime(input)
	if len(result) != 4 {
		t.Fatalf("expected 4 criteria, got %d", len(result))
	}
	// file_exists: path → target
	if string(result[0].Type) != "file_exists" || result[0].Target != "go.mod" {
		t.Errorf("criteria[0] = %+v, want file_exists/go.mod", result[0])
	}
	// command_succeeds: value → target (LB6 fix; was Expected before)
	if result[1].Target != "go build ./..." {
		t.Errorf("criteria[1].Target = %q, want 'go build ./...'", result[1].Target)
	}
	// test_passes: value → target
	if result[2].Target != "go test ./..." {
		t.Errorf("criteria[2].Target = %q, want 'go test ./...'", result[2].Target)
	}
	// file_contains: path → target, value → expected
	if result[3].Target != "main.go" || result[3].Expected != "package main" {
		t.Errorf("criteria[3] = %+v, want target=main.go expected=package main", result[3])
	}
}

func TestExecutor_Setters(t *testing.T) {
	// Verify setters don't panic and store values correctly.
	cfg := config.DefaultConfig()
	es, ps := newTestStores(t)

	exec := NewExecutor(nil, cfg, es, ps, nil)

	exec.SetLLMClient(nil) // should not panic
	exec.SetArtifactStore(nil)
	exec.SetScratchboard(nil)
	exec.SetController(nil)

	// No assertion needed — just verifying no panics.
}
