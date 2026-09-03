package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(path, []byte("version: \"1.0\"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultConfig_EventLogDurabilityDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Workspace.MaxEventBytes != 1<<20 {
		t.Errorf("max_event_bytes default = %d, want 1 MiB", cfg.Workspace.MaxEventBytes)
	}
	if !cfg.Workspace.FsyncEvents {
		t.Error("fsync_events should default to true")
	}
}

func TestLoadFromFile_DurabilityKeysOverride(t *testing.T) {
	path := writeCfg(t, t.TempDir(), "workspace:\n  max_event_bytes: 4096\n  fsync_events: false\n")
	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace.MaxEventBytes != 4096 || cfg.Workspace.FsyncEvents {
		t.Errorf("got max=%d fsync=%v", cfg.Workspace.MaxEventBytes, cfg.Workspace.FsyncEvents)
	}
}

func TestValidate_NegativeMaxEventBytes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workspace.MaxEventBytes = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative max_event_bytes must be rejected")
	}
}

// Defect 5(a): state_dir is normalised at load time so every consumer sees
// an absolute, ~-expanded path (the report builder used to join the raw ~).
func TestLoadFromFile_StateDirNormalised(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := t.TempDir()
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"default tilde", "", filepath.Join(home, ".nxd")},
		{"explicit tilde", "workspace:\n  state_dir: ~/custom\n", filepath.Join(home, "custom")},
		{"relative resolves against config dir", "workspace:\n  state_dir: .nxd\n", filepath.Join(dir, ".nxd")},
		{"relative nested", "workspace:\n  state_dir: ./var/nxd\n", filepath.Join(dir, "var", "nxd")},
		{"absolute untouched", "workspace:\n  state_dir: /abs/path\n", "/abs/path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCfg(t, dir, tc.yaml)
			cfg, err := config.LoadFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workspace.StateDir != tc.want {
				t.Errorf("StateDir = %q, want %q", cfg.Workspace.StateDir, tc.want)
			}
			if !filepath.IsAbs(cfg.Workspace.StateDir) {
				t.Errorf("StateDir must be absolute after load")
			}
		})
	}
}

func TestNormalizeStateDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		dir, base, want string
	}{
		{"", "/cfg", filepath.Join(home, ".nxd")},
		{"~", "/cfg", home},
		{"~/x", "/cfg", filepath.Join(home, "x")},
		{".nxd", "/cfg", "/cfg/.nxd"},
		{"/abs", "/cfg", "/abs"},
		{"rel", "", func() string { wd, _ := os.Getwd(); return filepath.Join(wd, "rel") }()},
	}
	for _, tc := range tests {
		if got := config.NormalizeStateDir(tc.dir, tc.base); got != tc.want {
			t.Errorf("NormalizeStateDir(%q,%q) = %q, want %q", tc.dir, tc.base, got, tc.want)
		}
	}
}

func TestLoadFromFile_OllamaHost(t *testing.T) {
	path := writeCfg(t, t.TempDir(), "models:\n  ollama_host: 10.0.0.5:11434\n")
	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.OllamaHost != "10.0.0.5:11434" {
		t.Errorf("OllamaHost = %q", cfg.Models.OllamaHost)
	}
}

func TestDefaultYAMLForWith_AppliesMutation(t *testing.T) {
	data, _, err := config.DefaultYAMLForWith(t.TempDir(), func(c *config.Config) { c.Workspace.StateDir = ".nxd" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "state_dir: .nxd") {
		t.Errorf("mutation not applied:\n%s", data)
	}
	plain, _, _ := config.DefaultYAMLForWith(t.TempDir(), nil)
	if !strings.Contains(string(plain), "state_dir: ~/.nxd") {
		t.Errorf("nil mutate should keep defaults")
	}
}
