package cli

import (
	"os"
	"strings"
	"testing"
)

// TestResume_WiresSandbox guards the sandbox against the dead-wire class: the
// CommandSandbox only confines run_command / criteria / investigator commands
// if runResume installs it from config before agents are spawned.
func TestResume_WiresSandbox(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	if !strings.Contains(string(src), "runtime.InstallSandbox(s.Config.Sandbox") {
		t.Error("resume.go must call runtime.InstallSandbox(s.Config.Sandbox, ...)")
	}
}
