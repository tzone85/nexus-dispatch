package cli

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
)

// TestResume_WiresSandbox guards the sandbox against the dead-wire class: the
// CommandSandbox only confines run_command / criteria / investigator commands
// if runResume installs it from config before agents are spawned.
func TestResume_WiresSandbox(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	if !strings.Contains(string(src), "installSandbox(s.Config, out)") {
		t.Error("resume.go must call installSandbox(s.Config, out)")
	}
	// The investigator also runs commands: nxd req / nxd plan must install
	// the sandbox before constructing it.
	for file, want := range map[string]string{
		"req.go":  "installSandbox(s.Config, out)",
		"plan.go": "installSandbox(cfg, out)",
	} {
		code, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(code), want) {
			t.Errorf("%s must call %s before NewInvestigator", file, want)
		}
	}
}

func TestInstallSandbox_HostModeAndDockerFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sandbox.Mode = "host"
	var out bytes.Buffer
	if err := installSandbox(cfg, &out); err != nil || out.Len() != 0 {
		t.Fatalf("host mode: err=%v out=%q", err, out.String())
	}
	if runtime.DefaultSandbox().Name() != "host" {
		t.Error("host sandbox must be installed")
	}
	cfg.Sandbox.Mode = "docker"
	err := installSandbox(cfg, &out)
	if dockerUp := exec.Command("docker", "info").Run() == nil; !dockerUp {
		if err == nil || !strings.Contains(err.Error(), "sandbox:") {
			t.Errorf("docker mode without docker must fail: %v", err)
		}
	}
	t.Cleanup(func() { runtime.SetDefaultSandbox(nil); criteria.SetCommandExecutor(nil) })
}
