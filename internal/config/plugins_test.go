package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestDefaultConfig_PluginsEmpty(t *testing.T) {
	cfg := config.DefaultConfig()

	if len(cfg.Plugins.Playbooks) != 0 {
		t.Errorf("expected empty Playbooks, got %d", len(cfg.Plugins.Playbooks))
	}
	if len(cfg.Plugins.Prompts) != 0 {
		t.Errorf("expected empty Prompts, got %d", len(cfg.Plugins.Prompts))
	}
	if len(cfg.Plugins.QA) != 0 {
		t.Errorf("expected empty QA, got %d", len(cfg.Plugins.QA))
	}
	if len(cfg.Plugins.Providers) != 0 {
		t.Errorf("expected empty Providers, got %d", len(cfg.Plugins.Providers))
	}
}

func TestLoadFromFile_WithPlugins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.config.yaml")
	os.WriteFile(path, []byte(`
plugins:
  playbooks:
    - name: deploy-checklist
      file: playbooks/deploy.md
      inject_when: pre_merge
      roles: [senior, tech_lead]
  prompts:
    code_review: "Review this code for security issues"
  qa:
    - name: lint-check
      file: qa/lint.sh
      after: build
  providers:
    local-llama:
      command: llama-server
      models: [llama3, codellama]
`), 0644)

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.Plugins.Playbooks) != 1 {
		t.Fatalf("expected 1 playbook, got %d", len(cfg.Plugins.Playbooks))
	}
	pb := cfg.Plugins.Playbooks[0]
	if pb.Name != "deploy-checklist" {
		t.Errorf("expected playbook name 'deploy-checklist', got %s", pb.Name)
	}
	if pb.File != "playbooks/deploy.md" {
		t.Errorf("expected playbook file 'playbooks/deploy.md', got %s", pb.File)
	}
	if pb.InjectWhen != "pre_merge" {
		t.Errorf("expected inject_when 'pre_merge', got %s", pb.InjectWhen)
	}
	if len(pb.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(pb.Roles))
	}

	if cfg.Plugins.Prompts["code_review"] != "Review this code for security issues" {
		t.Errorf("unexpected prompt value: %s", cfg.Plugins.Prompts["code_review"])
	}

	if len(cfg.Plugins.QA) != 1 {
		t.Fatalf("expected 1 QA config, got %d", len(cfg.Plugins.QA))
	}
	qa := cfg.Plugins.QA[0]
	if qa.Name != "lint-check" {
		t.Errorf("expected QA name 'lint-check', got %s", qa.Name)
	}
	if qa.After != "build" {
		t.Errorf("expected QA after 'build', got %s", qa.After)
	}

	prov, ok := cfg.Plugins.Providers["local-llama"]
	if !ok {
		t.Fatal("expected 'local-llama' provider")
	}
	if prov.Command != "llama-server" {
		t.Errorf("expected provider command 'llama-server', got %s", prov.Command)
	}
	if len(prov.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(prov.Models))
	}
}

func TestDefaultConfig_PluginsValidates(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config with empty plugins should validate: %v", err)
	}
}
