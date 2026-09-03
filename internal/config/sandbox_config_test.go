package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestDefaultConfig_SandboxAndApprovalsDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Sandbox.Mode != "auto" || cfg.Sandbox.Network != "none" || cfg.Sandbox.Image != "golang:1.26-alpine" {
		t.Errorf("sandbox defaults = %+v", cfg.Sandbox)
	}
	if cfg.Sandbox.CPUs != "2" || cfg.Sandbox.Memory != "2g" || cfg.Sandbox.AutoApprovePrompts != nil {
		t.Errorf("sandbox limits = %+v", cfg.Sandbox)
	}
	want := []string{"conflict_resolution", "integration_failure", "security_finding"}
	if !reflect.DeepEqual(cfg.Approvals.RequireFor, want) || cfg.Approvals.TimeoutAction != "pause" {
		t.Errorf("approvals defaults = %+v", cfg.Approvals)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestDefaultConfig_NoUnattendedFlagsOnHost(t *testing.T) {
	cfg := config.DefaultConfig()
	for name, flag := range map[string]string{"claude-code": "--dangerously-skip-permissions", "codex": "--full-auto"} {
		rt := cfg.Runtimes[name]
		for _, a := range rt.Args {
			if a == flag {
				t.Errorf("%s default args must not contain %s: %v", name, flag, rt.Args)
			}
		}
		if got := rt.EffectiveArgs(); len(got) != len(rt.Args) {
			t.Errorf("%s on host: EffectiveArgs=%v, want unchanged %v", name, got, rt.Args)
		}
	}
}

func TestRuntimeConfig_EffectiveArgs(t *testing.T) {
	cases := []struct {
		name string
		rc   config.RuntimeConfig
		want []string
	}{
		{"claude host", config.RuntimeConfig{Command: "claude", Args: []string{"-v"}}, []string{"-v"}},
		{"claude docker", config.RuntimeConfig{Command: "claude", Args: []string{"-v"}, Runner: "docker"}, []string{"-v", "--dangerously-skip-permissions"}},
		{"codex ssh", config.RuntimeConfig{Command: "codex", Runner: "ssh"}, []string{"--full-auto"}},
		{"codex docker already present", config.RuntimeConfig{Command: "codex", Args: []string{"--full-auto"}, Runner: "docker"}, []string{"--full-auto"}},
		{"unknown cli docker", config.RuntimeConfig{Command: "aider", Args: []string{"--x"}, Runner: "docker"}, []string{"--x"}},
		{"tmux explicit", config.RuntimeConfig{Command: "claude", Runner: "tmux"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rc.EffectiveArgs()
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EffectiveArgs = %v, want %v", got, tc.want)
			}
		})
	}
	// EffectiveArgs must not alias the config slice.
	rc := config.RuntimeConfig{Command: "claude", Args: make([]string, 1, 4), Runner: "docker"}
	_ = rc.EffectiveArgs()
	if rc.Args[0] != "" || len(rc.Args) != 1 {
		t.Errorf("EffectiveArgs mutated Args: %v", rc.Args)
	}
}

func TestConfig_UnsandboxedRuntimes(t *testing.T) {
	cfg := config.DefaultConfig()
	got := cfg.UnsandboxedRuntimes()
	want := []string{"aider", "claude-code", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnsandboxedRuntimes = %v, want %v (native gemma excluded)", got, want)
	}
	rt := cfg.Runtimes["codex"]
	rt.Runner = "docker"
	cfg.Runtimes["codex"] = rt
	if got := cfg.UnsandboxedRuntimes(); reflect.DeepEqual(got, want) {
		t.Errorf("docker runner must drop codex from the list, got %v", got)
	}
}

func TestConfig_AutoApprovePrompts(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.AutoApprovePrompts("claude-code") {
		t.Error("host runtime must default to false")
	}
	if cfg.AutoApprovePrompts("missing") {
		t.Error("unknown runtime must be false")
	}
	rt := cfg.Runtimes["claude-code"]
	rt.Runner = "docker"
	cfg.Runtimes["claude-code"] = rt
	if !cfg.AutoApprovePrompts("claude-code") {
		t.Error("sandboxed runtime must default to true")
	}
	f := false
	cfg.Sandbox.AutoApprovePrompts = &f
	if cfg.AutoApprovePrompts("claude-code") {
		t.Error("explicit false must win")
	}
	tr := true
	cfg.Sandbox.AutoApprovePrompts = &tr
	rt.Runner = ""
	cfg.Runtimes["claude-code"] = rt
	if !cfg.AutoApprovePrompts("claude-code") {
		t.Error("explicit true must win")
	}
}

func TestValidate_SandboxAndApprovals(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *config.Config)
		wantErr string
	}{
		{"bad mode", func(c *config.Config) { c.Sandbox.Mode = "vm" }, "sandbox.mode"},
		{"bad network", func(c *config.Config) { c.Sandbox.Network = "host" }, "sandbox.network"},
		{"empty image", func(c *config.Config) { c.Sandbox.Image = " " }, "sandbox.image"},
		{"bad mount", func(c *config.Config) { c.Sandbox.ExtraMounts = []string{"/abs:/x"} }, "sandbox.extra_mounts[0]"},
		{"good mount", func(c *config.Config) { c.Sandbox.ExtraMounts = []string{"vendor:/vendor:ro"} }, ""},
		{"host mode ok", func(c *config.Config) { c.Sandbox.Mode = "host"; c.Sandbox.Network = "bridge" }, ""},
		{"bad approval kind", func(c *config.Config) { c.Approvals.RequireFor = []string{"nope"} }, "approvals.require_for"},
		{"merge kind ok", func(c *config.Config) { c.Approvals.RequireFor = []string{"merge"} }, ""},
		{"bad timeout action", func(c *config.Config) { c.Approvals.TimeoutAction = "abort" }, "approvals.timeout_action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateExtraMount(t *testing.T) {
	cases := map[string]bool{
		"vendor:/vendor":         true,
		"vendor:/vendor:ro":      true,
		".cache/go:/root/.cache": true,
		"/abs:/x":                false,
		"~/x:/x":                 false,
		"../x:/x":                false,
		"a/../../x:/x":           false,
		"vendor:relative":        false,
		"vendor:/x/../etc":       false,
		"vendor:/x:rw":           false,
		"vendor":                 false,
		":/x":                    false,
		"a:/b:ro:extra":          false,
	}
	for m, ok := range cases {
		err := config.ValidateExtraMount(m)
		if (err == nil) != ok {
			t.Errorf("ValidateExtraMount(%q) = %v, want ok=%v", m, err, ok)
		}
	}
}

func TestApprovalsConfig_Requires(t *testing.T) {
	a := config.ApprovalsConfig{RequireFor: []string{"merge"}}
	if !a.Requires("merge") || a.Requires("security_finding") {
		t.Errorf("Requires mismatch: %+v", a)
	}
}

func TestLoadFromFile_SandboxOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(path, []byte(`
version: "1.0"
sandbox:
  mode: host
  auto_approve_prompts: true
approvals:
  require_for: [security_finding]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Sandbox.Mode != "host" || cfg.Sandbox.Image != "golang:1.26-alpine" {
		t.Errorf("overlay lost defaults: %+v", cfg.Sandbox)
	}
	if cfg.Sandbox.AutoApprovePrompts == nil || !*cfg.Sandbox.AutoApprovePrompts {
		t.Errorf("auto_approve_prompts not parsed: %+v", cfg.Sandbox.AutoApprovePrompts)
	}
	if !reflect.DeepEqual(cfg.Approvals.RequireFor, []string{"security_finding"}) {
		t.Errorf("require_for = %v", cfg.Approvals.RequireFor)
	}
}
