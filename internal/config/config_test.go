package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Workspace.Backend != "sqlite" {
		t.Fatalf("expected backend 'sqlite', got %s", cfg.Workspace.Backend)
	}
	if cfg.Routing.JuniorMaxComplexity != 3 {
		t.Fatalf("expected junior max 3, got %d", cfg.Routing.JuniorMaxComplexity)
	}
	if cfg.Routing.IntermediateMaxComplexity != 5 {
		t.Fatalf("expected intermediate max 5, got %d", cfg.Routing.IntermediateMaxComplexity)
	}
	if cfg.Merge.AutoMerge != true {
		t.Fatal("expected auto_merge true")
	}
	if cfg.Merge.BaseBranch != "main" {
		t.Fatalf("expected base_branch 'main', got %s", cfg.Merge.BaseBranch)
	}
	if cfg.Merge.Mode != "local" {
		t.Fatalf("expected merge mode 'local', got %s", cfg.Merge.Mode)
	}
	if cfg.Cleanup.LogArchive != "file" {
		t.Fatalf("expected log_archive 'file', got %s", cfg.Cleanup.LogArchive)
	}
	if cfg.Monitor.PollIntervalMs != 10000 {
		t.Fatalf("expected poll_interval 10000, got %d", cfg.Monitor.PollIntervalMs)
	}
}

func TestDefaultConfig_Validates(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.config.yaml")
	os.WriteFile(path, []byte(`
version: "1.0"
workspace:
  backend: sqlite
  log_level: debug
routing:
  junior_max_complexity: 5
  intermediate_max_complexity: 8
`), 0644)

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Workspace.Backend != "sqlite" {
		t.Fatalf("expected 'sqlite', got %s", cfg.Workspace.Backend)
	}
	if cfg.Workspace.LogLevel != "debug" {
		t.Fatalf("expected 'debug', got %s", cfg.Workspace.LogLevel)
	}
	if cfg.Routing.JuniorMaxComplexity != 5 {
		t.Fatalf("expected 5, got %d", cfg.Routing.JuniorMaxComplexity)
	}
	// Defaults should still be present for unset fields
	if cfg.Monitor.PollIntervalMs != 10000 {
		t.Fatalf("expected default poll_interval 10000, got %d", cfg.Monitor.PollIntervalMs)
	}
}

func TestLoadFromFile_InvalidBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.config.yaml")
	os.WriteFile(path, []byte(`
workspace:
  backend: postgres
`), 0644)

	_, err := config.LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for invalid backend")
	}
}

func TestLoadFromFile_InvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.config.yaml")
	os.WriteFile(path, []byte(`
workspace:
  log_level: verbose
`), 0644)

	_, err := config.LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for invalid log level")
	}
}

func TestValidation_ComplexityRange(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Routing.JuniorMaxComplexity = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for complexity 0")
	}

	cfg = config.DefaultConfig()
	cfg.Routing.JuniorMaxComplexity = 14
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for complexity 14")
	}
}

func TestValidation_IntermediateGteJunior(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Routing.JuniorMaxComplexity = 5
	cfg.Routing.IntermediateMaxComplexity = 3
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error: intermediate < junior")
	}
}

func TestLoadFromFile_WithRuntimes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.config.yaml")
	os.WriteFile(path, []byte(`
runtimes:
  claude-code:
    command: claude
    args: ["--dangerously-skip-permissions"]
    models: ["opus-4", "sonnet-4"]
    detection:
      idle_pattern: "^\\$\\s*$"
      permission_pattern: "\\[Y/n\\]"
`), 0644)

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rt, ok := cfg.Runtimes["claude-code"]
	if !ok {
		t.Fatal("expected claude-code runtime")
	}
	if rt.Command != "claude" {
		t.Fatalf("expected command 'claude', got %s", rt.Command)
	}
	if len(rt.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(rt.Models))
	}
}

func TestLoadFromFile_FileNotFound(t *testing.T) {
	_, err := config.LoadFromFile("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidation_InvalidMergeMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Merge.Mode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid merge mode")
	}
}

func TestValidation_ValidMergeModes(t *testing.T) {
	for _, mode := range []string{"local", "github"} {
		cfg := config.DefaultConfig()
		cfg.Merge.Mode = mode
		if err := cfg.Validate(); err != nil {
			t.Fatalf("merge mode %q should be valid: %v", mode, err)
		}
	}
}

func TestDefaultConfig_IncludesModels(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Models.TechLead.Provider != "google+ollama" {
		t.Fatalf("expected tech_lead provider 'google+ollama', got %s", cfg.Models.TechLead.Provider)
	}
	if cfg.Models.Junior.Provider != "google+ollama" {
		t.Fatalf("expected junior provider 'google+ollama', got %s", cfg.Models.Junior.Provider)
	}
	if cfg.Models.TechLead.Model != "gemma4:26b" {
		t.Fatalf("expected tech_lead model 'gemma4:26b', got %s", cfg.Models.TechLead.Model)
	}
	if cfg.Models.TechLead.MaxTokens != 16000 {
		t.Fatalf("expected tech_lead max_tokens 16000, got %d", cfg.Models.TechLead.MaxTokens)
	}
}

func TestDefaultConfig_IncludesRuntimes(t *testing.T) {
	cfg := config.DefaultConfig()

	if len(cfg.Runtimes) != 4 {
		t.Fatalf("expected 4 runtimes, got %d", len(cfg.Runtimes))
	}
	rt, ok := cfg.Runtimes["aider"]
	if !ok {
		t.Fatal("expected aider runtime in defaults")
	}
	if rt.Command != "aider" {
		t.Fatalf("expected command 'aider', got %s", rt.Command)
	}
}

func TestDefaultConfig_IncludesPRTemplate(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Merge.PRTemplate == "" {
		t.Fatal("expected non-empty PR template in defaults")
	}
}

func TestDefaultYAML_RoundTrip(t *testing.T) {
	data, err := config.DefaultYAML()
	if err != nil {
		t.Fatalf("DefaultYAML: %v", err)
	}

	// Write to a temp file and load it back — should produce a valid config
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile on generated YAML: %v", err)
	}

	// Verify key fields survived the roundtrip
	if cfg.Version != "1.0" {
		t.Fatalf("expected version '1.0', got %s", cfg.Version)
	}
	if cfg.Workspace.Backend != "sqlite" {
		t.Fatalf("expected backend 'sqlite', got %s", cfg.Workspace.Backend)
	}
	if cfg.Models.TechLead.Provider != "google+ollama" {
		t.Fatalf("expected tech_lead provider 'google+ollama', got %s", cfg.Models.TechLead.Provider)
	}
	if len(cfg.Runtimes) != 4 {
		t.Fatalf("expected 4 runtimes, got %d", len(cfg.Runtimes))
	}
	if cfg.Merge.PRTemplate == "" {
		t.Fatal("expected non-empty PR template after roundtrip")
	}
	if cfg.Merge.Mode != "local" {
		t.Fatalf("expected merge mode 'local', got %s", cfg.Merge.Mode)
	}
}

func TestDefaultYAML_HasHeader(t *testing.T) {
	data, err := config.DefaultYAML()
	if err != nil {
		t.Fatalf("DefaultYAML: %v", err)
	}

	header := "# NXD configuration"
	if len(data) < len(header) || string(data[:len(header)]) != header {
		t.Fatalf("expected YAML to start with %q, got %q", header, string(data[:40]))
	}
}

func TestValidation_GoogleProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models.TechLead.Provider = "google+ollama"
	cfg.Models.TechLead.GoogleModel = "gemma-4-26b-a4b-it"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid google+ollama config rejected: %v", err)
	}
}

func TestValidation_GoogleProvider_MissingGoogleModel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models.TechLead.Provider = "google+ollama"
	cfg.Models.TechLead.GoogleModel = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for google provider without google_model")
	}
}

func TestValidation_NativeRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	gemmaRT, ok := cfg.Runtimes["gemma"]
	if !ok {
		t.Fatal("expected 'gemma' runtime in defaults")
	}
	if !gemmaRT.Native {
		t.Error("expected gemma runtime to be native")
	}
	if gemmaRT.MaxIterations <= 0 {
		t.Errorf("MaxIterations = %d, want > 0", gemmaRT.MaxIterations)
	}
	if len(gemmaRT.CommandAllowlist) == 0 {
		t.Error("expected non-empty command allowlist")
	}
}

func TestDefaultConfig_UpdateCheckDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Workspace.UpdateCheck {
		t.Error("expected UpdateCheck=true by default")
	}
	if cfg.Workspace.UpdateIntervalHours != 48 {
		t.Errorf("UpdateIntervalHours = %d, want 48", cfg.Workspace.UpdateIntervalHours)
	}
}

func TestValidation_NegativeUpdateInterval(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workspace.UpdateIntervalHours = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative update_interval_hours")
	}
}

func TestValidation_ZeroUpdateInterval(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workspace.UpdateIntervalHours = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected zero interval to pass validation, got: %v", err)
	}
}

func TestDefaultConfig_MemoryDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Memory.Enabled {
		t.Error("expected Memory.Enabled=true by default")
	}
}

func TestDefaultConfig_SafetyDefaults(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Merge.ReviewBeforeMerge {
		t.Error("expected ReviewBeforeMerge=false by default")
	}

	if len(cfg.Investigation.CommandAllowlist) == 0 {
		t.Error("expected non-empty Investigation.CommandAllowlist by default")
	}
}
