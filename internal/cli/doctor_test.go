package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestDoctorCmd_Registers(t *testing.T) {
	cmd := newDoctorCmd()
	if cmd.Use != "doctor" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "doctor")
	}
	if cmd.Short == "" {
		t.Fatal("Short description must not be empty")
	}
	if cmd.RunE == nil {
		t.Fatal("RunE must be set")
	}
}

func TestDoctorCmd_PrintsHeader(t *testing.T) {
	cmd := newDoctorCmd()

	// Add the config flag that runDoctor expects (normally inherited from root).
	cmd.Flags().String("config", "", "Path to config file")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// The command may return an error (e.g. if Ollama is not running),
	// but we only care that it produces output and doesn't panic.
	_ = cmd.Execute()

	output := buf.String()
	if !strings.Contains(output, "NXD Doctor") {
		t.Fatalf("expected header in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Results:") {
		t.Fatalf("expected results summary in output, got:\n%s", output)
	}
}

func TestDoctorCmd_CheckResultCounts(t *testing.T) {
	cmd := newDoctorCmd()
	cmd.Flags().String("config", "", "Path to config file")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	_ = cmd.Execute()

	output := buf.String()

	// Verify every check category appears in the output.
	expectedChecks := []string{
		"Go",
		"Git",
		"tmux",
		"Ollama",
		"Gemma 4 model",
		"Config",
		"State directory",
		"MemPalace",
		"Google AI API",
		"Plugins",
		"Disk/permissions",
	}
	for _, name := range expectedChecks {
		if !strings.Contains(output, name) {
			t.Errorf("missing check %q in output", name)
		}
	}
}

func TestDoctorCmd_IndividualCheckFunctions(t *testing.T) {
	// Verify each check function returns a valid checkResult and doesn't panic.
	// The actual status depends on the system, but the structure must be correct.
	validStatuses := map[string]bool{"ok": true, "warn": true, "fail": true}

	checks := []struct {
		name string
		fn   func() checkResult
	}{
		{"checkGo", checkGo},
		{"checkGit", checkGit},
		{"checkTmux", checkTmux},
		{"checkOllamaRunning", checkOllamaRunning},
		{"checkGemmaModel", checkGemmaModel},
		{"checkMemPalace", checkMemPalace},
		{"checkGoogleAI", checkGoogleAI},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.fn()
			if result.Name == "" {
				t.Error("Name must not be empty")
			}
			if !validStatuses[result.Status] {
				t.Errorf("Status = %q, want one of ok/warn/fail", result.Status)
			}
			if result.Message == "" {
				t.Error("Message must not be empty")
			}
		})
	}
}

func TestCheckDevDB_NullProviderIsOK(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "null"
	r := checkDevDB(cfg)
	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok for null provider", r.Status)
	}
	if !strings.Contains(r.Message, "not configured") {
		t.Errorf("Message should mention not-configured: %q", r.Message)
	}
}

func TestCheckDevDB_EmptyProviderIsOK(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = ""
	r := checkDevDB(cfg)
	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok when devdb absent", r.Status)
	}
}

func TestCheckDevDB_UnsupportedProviderFails(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "ghost"
	r := checkDevDB(cfg)
	if r.Status != "fail" {
		t.Errorf("Status = %q, want fail for unsupported provider", r.Status)
	}
}

func TestCheckDevDB_DockerUnreachableFails(t *testing.T) {
	// Point the docker client at a guaranteed-bogus socket via DOCKER_HOST,
	// which NewClient honours. The Docker.Host config field is the *Postgres*
	// DSN host, not the docker daemon URL — they share a name but not a
	// purpose. t.Setenv restores the env on test exit.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent-nxd-test.sock")

	cfg := config.Config{}
	cfg.DevDB.Provider = "docker"
	r := checkDevDB(cfg)
	if r.Status != "fail" {
		t.Errorf("Status = %q, want fail for unreachable docker daemon", r.Status)
	}
	if !strings.Contains(r.Message, "unreachable") {
		t.Errorf("Message should mention unreachable: %q", r.Message)
	}
}

func TestDoctorCmd_RootRegistration(t *testing.T) {
	// Verify doctor is registered as a subcommand of root.
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "doctor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("doctor command not registered on rootCmd")
	}
}
