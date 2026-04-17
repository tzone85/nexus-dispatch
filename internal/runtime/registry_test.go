package runtime_test

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
)

func TestNewRegistry(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"claude-code": {
			Command: "claude",
			Args:    []string{"--dangerously-skip-permissions"},
			Models:  []string{"opus-4", "sonnet-4"},
			Detection: config.RuntimeDetection{
				IdlePattern:       `^\$\s*$`,
				PermissionPattern: `\[Y/n\]`,
			},
		},
		"codex": {
			Command: "codex",
			Args:    []string{"--approval-mode", "full-auto"},
			Models:  []string{"o3"},
			Detection: config.RuntimeDetection{
				IdlePattern: "Codex>",
			},
		},
	}

	reg, err := runtime.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	names := reg.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 runtimes, got %d", len(names))
	}
}

func TestRegistry_Get(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"claude-code": {
			Command: "claude",
			Models:  []string{"opus-4", "sonnet-4"},
			Detection: config.RuntimeDetection{
				IdlePattern: `\$`,
			},
		},
	}

	reg, err := runtime.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	rt, err := reg.Get("claude-code")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rt.Name() != "claude-code" {
		t.Fatalf("expected name 'claude-code', got %s", rt.Name())
	}
	if len(rt.SupportedModels()) != 2 {
		t.Fatalf("expected 2 models, got %d", len(rt.SupportedModels()))
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing runtime")
	}
}

func TestRegistry_InvalidPattern(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"bad": {
			Command: "bad",
			Detection: config.RuntimeDetection{
				IdlePattern: "[invalid",
			},
		},
	}
	_, err := runtime.NewRegistry(cfg)
	if err == nil {
		t.Fatal("expected error for invalid regex pattern")
	}
}

func TestRegistry_IsNative_True(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"gemma": {Native: true, Models: []string{"gemma4"}},
	}
	reg, err := runtime.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if !reg.IsNative("gemma") {
		t.Error("expected gemma to be native")
	}
	if reg.IsNative("nonexistent") {
		t.Error("expected nonexistent to not be native")
	}
}

func TestRegistry_NativeConfig_Found(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"gemma": {
			Native:  true,
			Models:  []string{"gemma4"},
			Command: "ollama",
		},
	}
	reg, err := runtime.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	rc, ok := reg.NativeConfig("gemma")
	if !ok {
		t.Fatal("expected to find native config for gemma")
	}
	if !rc.Native {
		t.Error("expected rc.Native to be true")
	}
}

func TestRegistry_NativeConfig_NotFound(t *testing.T) {
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	_, ok := reg.NativeConfig("nonexistent")
	if ok {
		t.Error("expected NativeConfig to return false for nonexistent runtime")
	}
}

func TestRegistry_List_IncludesNative(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"gemma":       {Native: true},
		"claude-code": {Command: "claude"},
	}
	reg, err := runtime.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	names := reg.List()
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["gemma"] {
		t.Error("expected 'gemma' in List()")
	}
	if !found["claude-code"] {
		t.Error("expected 'claude-code' in List()")
	}
}

func TestRegistry_InvalidPermissionPattern(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"bad": {
			Command: "bad",
			Detection: config.RuntimeDetection{
				PermissionPattern: "[invalid",
			},
		},
	}
	_, err := runtime.NewRegistry(cfg)
	if err == nil {
		t.Fatal("expected error for invalid permission pattern")
	}
	if !strings.Contains(err.Error(), "permission pattern") {
		t.Errorf("error = %v, expected 'permission pattern'", err)
	}
}

func TestRegistry_InvalidPlanModePattern(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"bad": {
			Command: "bad",
			Detection: config.RuntimeDetection{
				PlanModePattern: "[invalid",
			},
		},
	}
	_, err := runtime.NewRegistry(cfg)
	if err == nil {
		t.Fatal("expected error for invalid plan mode pattern")
	}
	if !strings.Contains(err.Error(), "plan mode pattern") {
		t.Errorf("error = %v, expected 'plan mode pattern'", err)
	}
}

func TestAgentStatus_String(t *testing.T) {
	tests := []struct {
		status   runtime.AgentStatus
		expected string
	}{
		{runtime.StatusWorking, "working"},
		{runtime.StatusStuck, "stuck"},
		{runtime.StatusDone, "done"},
		{runtime.StatusPermissionPrompt, "permission_prompt"},
		{runtime.StatusPlanMode, "plan_mode"},
		{runtime.StatusTerminated, "terminated"},
	}
	for _, tt := range tests {
		if tt.status.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.status.String())
		}
	}
}
