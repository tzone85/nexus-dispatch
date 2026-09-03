package cli

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestCheckSandbox(t *testing.T) {
	up := func() bool { return true }
	down := func() bool { return false }
	sandboxed := config.DefaultConfig()
	for name, rt := range sandboxed.Runtimes {
		if !rt.Native {
			rt.Runner = "docker"
			rt.Docker.Image = "img"
			sandboxed.Runtimes[name] = rt
		}
	}

	cases := []struct {
		name       string
		cfg        func() config.Config
		docker     func() bool
		wantStatus string
		wantMsg    []string
	}{
		{"auto+docker but host runtimes", config.DefaultConfig, up, "warn",
			[]string{"run in docker", "agents run unsandboxed on this host: aider, claude-code, codex"}},
		{"auto no docker", config.DefaultConfig, down, "warn",
			[]string{"docker is unavailable", "unsandboxed"}},
		{"host mode", func() config.Config { c := config.DefaultConfig(); c.Sandbox.Mode = "host"; return c }, up, "warn",
			[]string{"sandbox.mode: host"}},
		{"docker mode no daemon", func() config.Config { c := config.DefaultConfig(); c.Sandbox.Mode = "docker"; return c }, down, "fail",
			[]string{"docker info` failed"}},
		{"docker mode, all runtimes sandboxed", func() config.Config { c := sandboxed; c.Sandbox.Mode = "docker"; return c }, up, "ok",
			[]string{"run in docker (golang:1.26-alpine, network none)"}},
		{"auto, all runtimes sandboxed", func() config.Config { return sandboxed }, up, "ok", nil},
		{"empty config", func() config.Config { return config.Config{} }, up, "warn", []string{"No config loaded"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkSandbox(tc.cfg(), tc.docker)
			if res.Name != "Sandbox" || res.Status != tc.wantStatus {
				t.Errorf("result = %+v, want status %s", res, tc.wantStatus)
			}
			for _, m := range tc.wantMsg {
				if !strings.Contains(res.Message, m) {
					t.Errorf("message %q missing %q", res.Message, m)
				}
			}
			if tc.wantStatus == "ok" && strings.Contains(res.Message, "unsandboxed") {
				t.Errorf("ok result must not mention unsandboxed: %q", res.Message)
			}
		})
	}
}
