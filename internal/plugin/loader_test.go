package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestEmptyConfig_ReturnsEmptyManager(t *testing.T) {
	cfg := config.PluginConfig{}
	mgr, err := LoadPlugins(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.Playbooks) != 0 {
		t.Errorf("expected 0 playbooks, got %d", len(mgr.Playbooks))
	}
	if len(mgr.Prompts) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(mgr.Prompts))
	}
	if len(mgr.QAChecks) != 0 {
		t.Errorf("expected 0 qa checks, got %d", len(mgr.QAChecks))
	}
	if len(mgr.Providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(mgr.Providers))
	}
}

func TestPlaybookLoading(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pbDir, "test.md"), []byte("# Test Playbook\nDo the thing."), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "test-playbook", File: "test.md", InjectWhen: "always", Roles: []string{"backend"}},
		},
	}
	mgr, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.Playbooks) != 1 {
		t.Fatalf("expected 1 playbook, got %d", len(mgr.Playbooks))
	}
	pb := mgr.Playbooks[0]
	if pb.Name != "test-playbook" {
		t.Errorf("expected name %q, got %q", "test-playbook", pb.Name)
	}
	if pb.Content != "# Test Playbook\nDo the thing." {
		t.Errorf("unexpected content: %q", pb.Content)
	}
	if pb.InjectWhen != "always" {
		t.Errorf("expected inject_when %q, got %q", "always", pb.InjectWhen)
	}
	if len(pb.Roles) != 1 || pb.Roles[0] != "backend" {
		t.Errorf("expected roles [backend], got %v", pb.Roles)
	}
}

func TestPromptOverrideLoading(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "system.txt"), []byte("You are a helpful assistant."), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.PluginConfig{
		Prompts: map[string]string{
			"system_prompt": "system.txt",
		},
	}
	mgr, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(mgr.Prompts))
	}
	got, ok := mgr.Prompts["system_prompt"]
	if !ok {
		t.Fatal("missing key system_prompt")
	}
	if got != "You are a helpful assistant." {
		t.Errorf("unexpected prompt content: %q", got)
	}
}

func TestQACheckLoading(t *testing.T) {
	dir := t.TempDir()
	qaDir := filepath.Join(dir, "qa")
	if err := os.MkdirAll(qaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qaDir, "lint.sh"), []byte("#!/bin/bash\necho ok"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.PluginConfig{
		QA: []config.PluginQAConfig{
			{Name: "lint", File: "lint.sh", After: "generate"},
		},
	}
	mgr, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.QAChecks) != 1 {
		t.Fatalf("expected 1 qa check, got %d", len(mgr.QAChecks))
	}
	qc := mgr.QAChecks[0]
	if qc.Name != "lint" {
		t.Errorf("expected name %q, got %q", "lint", qc.Name)
	}
	if qc.After != "generate" {
		t.Errorf("expected after %q, got %q", "generate", qc.After)
	}
	expected := filepath.Join(qaDir, "lint.sh")
	if qc.ScriptPath != expected {
		t.Errorf("expected script path %q, got %q", expected, qc.ScriptPath)
	}
}

func TestProviderLoading(t *testing.T) {
	cfg := config.PluginConfig{
		Providers: map[string]config.PluginProviderConfig{
			"ollama": {Command: "ollama serve", Models: []string{"gemma4:27b", "llama3"}},
		},
	}
	mgr, err := LoadPlugins(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(mgr.Providers))
	}
	p, ok := mgr.Providers["ollama"]
	if !ok {
		t.Fatal("missing provider ollama")
	}
	if p.Command != "ollama serve" {
		t.Errorf("expected command %q, got %q", "ollama serve", p.Command)
	}
	if len(p.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(p.Models))
	}
}

func TestMissingFile_ReturnsError(t *testing.T) {
	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "ghost", File: "nonexistent.md", InjectWhen: "always"},
		},
	}
	_, err := LoadPlugins(cfg, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestEmptyFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pbDir, "empty.md"), []byte("   \n  "), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "empty-pb", File: "empty.md", InjectWhen: "always"},
		},
	}
	_, err := LoadPlugins(cfg, dir)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestShouldInject_Always(t *testing.T) {
	pb := PluginPlaybook{InjectWhen: "always"}
	cases := []struct {
		role       string
		isExisting bool
		isBugFix   bool
		isInfra    bool
		want       bool
	}{
		{"backend", false, false, false, true},
		{"frontend", true, false, false, true},
		{"infra", false, false, true, true},
	}
	for _, tc := range cases {
		got := pb.ShouldInject(tc.role, tc.isExisting, tc.isBugFix, tc.isInfra)
		if got != tc.want {
			t.Errorf("ShouldInject(%q, %v, %v, %v) = %v, want %v",
				tc.role, tc.isExisting, tc.isBugFix, tc.isInfra, got, tc.want)
		}
	}
}

func TestShouldInject_Conditions(t *testing.T) {
	cases := []struct {
		name       string
		injectWhen string
		isExisting bool
		isBugFix   bool
		isInfra    bool
		want       bool
	}{
		{"existing match", "existing", true, false, false, true},
		{"existing no match", "existing", false, false, false, false},
		{"bugfix match", "bugfix", false, true, false, true},
		{"bugfix no match", "bugfix", false, false, false, false},
		{"infra match", "infra", false, false, true, true},
		{"infra no match", "infra", false, false, false, false},
		{"unknown condition", "nightly", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pb := PluginPlaybook{InjectWhen: tc.injectWhen}
			got := pb.ShouldInject("any", tc.isExisting, tc.isBugFix, tc.isInfra)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldInject_RoleFiltering(t *testing.T) {
	pb := PluginPlaybook{
		InjectWhen: "always",
		Roles:      []string{"backend", "infra"},
	}

	cases := []struct {
		role string
		want bool
	}{
		{"backend", true},
		{"Backend", true}, // case-insensitive
		{"infra", true},
		{"frontend", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			got := pb.ShouldInject(tc.role, false, false, false)
			if got != tc.want {
				t.Errorf("ShouldInject(%q) = %v, want %v", tc.role, got, tc.want)
			}
		})
	}
}

func TestShouldInject_EmptyRoles_AllowsAll(t *testing.T) {
	pb := PluginPlaybook{InjectWhen: "always", Roles: nil}
	if !pb.ShouldInject("anything", false, false, false) {
		t.Error("expected empty roles to allow all roles")
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	got := resolvePath("/plugins", "playbooks", "/absolute/path.md")
	if got != "/absolute/path.md" {
		t.Errorf("expected absolute path unchanged, got %q", got)
	}
}

func TestResolvePath_Relative(t *testing.T) {
	got := resolvePath("/plugins", "playbooks", "test.md")
	want := filepath.Join("/plugins", "playbooks", "test.md")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestEmptyManager(t *testing.T) {
	mgr := EmptyManager()
	if mgr.Playbooks != nil {
		t.Error("expected nil playbooks")
	}
	if mgr.Prompts == nil {
		t.Error("expected non-nil prompts map")
	}
	if mgr.QAChecks != nil {
		t.Error("expected nil qa checks")
	}
	if mgr.Providers == nil {
		t.Error("expected non-nil providers map")
	}
}

func TestProviderModels_AreCopied(t *testing.T) {
	original := []string{"model-a", "model-b"}
	cfg := config.PluginConfig{
		Providers: map[string]config.PluginProviderConfig{
			"test": {Command: "run", Models: original},
		},
	}
	mgr, err := LoadPlugins(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Mutating the original should not affect the loaded provider.
	original[0] = "mutated"
	if mgr.Providers["test"].Models[0] == "mutated" {
		t.Error("provider models should be a copy, not a reference to the original slice")
	}
}
