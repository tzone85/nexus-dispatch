package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// writeDoctorConfig writes a minimal valid nxd.yaml whose state dir points at
// stateDir and returns its path, so the doctor command loads config that
// resolves to a seeded fixture.
func writeDoctorConfig(t *testing.T, stateDir string) string {
	t.Helper()
	cfgContent := "version: \"1.0\"\nworkspace:\n  state_dir: " + stateDir +
		"\n  backend: sqlite\nmerge:\n  base_branch: main\n  mode: local\ncleanup:\n  branch_retention_days: 7\n"
	cfgPath := filepath.Join(t.TempDir(), "nxd.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// seedDriftFixture writes n events to events.jsonl and projects the first
// `projected` of them into nxd.db, leaving the projection watermark
// (projected) behind the log length (n) when projected < n. It returns a
// config whose state dir points at the fixture.
func seedDriftFixture(t *testing.T, n, projected int) config.Config {
	t.Helper()
	dir := t.TempDir()

	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(filepath.Join(dir, "nxd.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer ps.Close()

	for i := 0; i < n; i++ {
		evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
			"id":    fmt.Sprintf("req-%d", i),
			"title": "drift fixture requirement",
		})
		if err := es.Append(evt); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
		if i < projected {
			if err := ps.Project(evt); err != nil {
				t.Fatalf("project event %d: %v", i, err)
			}
		}
	}

	return config.Config{Workspace: config.WorkspaceConfig{StateDir: dir}}
}

func TestCheckProjectionDrift_ReportsDrift(t *testing.T) {
	cfg := seedDriftFixture(t, 5, 2) // log has 5, projection applied 2

	r := checkProjectionDrift(cfg)

	if r.Status != "warn" {
		t.Fatalf("Status = %q, want %q (message: %s)", r.Status, "warn", r.Message)
	}
	if !strings.Contains(r.Message, "behind") {
		t.Errorf("message %q should report the projection is behind the log", r.Message)
	}
	// The gap (3) should be surfaced so an operator knows how far behind.
	if !strings.Contains(r.Message, "3") {
		t.Errorf("message %q should surface the 3-event gap", r.Message)
	}
}

func TestCheckProjectionDrift_InSync(t *testing.T) {
	cfg := seedDriftFixture(t, 4, 4) // watermark matches the log

	r := checkProjectionDrift(cfg)

	if r.Status != "ok" {
		t.Fatalf("Status = %q, want %q (message: %s)", r.Status, "ok", r.Message)
	}
	if !strings.Contains(r.Message, "sync") {
		t.Errorf("message %q should report the projection is in sync", r.Message)
	}
}

// Failure path: an uninitialised state dir (no stores yet) must not create
// empty stores or fail the check — it reports ok and steps aside, letting the
// existing State-directory check own the "run nxd init" guidance.
func TestCheckProjectionDrift_NoStoresIsOK(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Workspace: config.WorkspaceConfig{StateDir: dir}}

	r := checkProjectionDrift(cfg)

	if r.Status != "ok" {
		t.Fatalf("Status = %q, want %q (message: %s)", r.Status, "ok", r.Message)
	}
	// The check must not have materialised stores as a side effect.
	if fileExistsAt(filepath.Join(dir, "events.jsonl")) {
		t.Error("checkProjectionDrift created events.jsonl as a side effect")
	}
	if fileExistsAt(filepath.Join(dir, "nxd.db")) {
		t.Error("checkProjectionDrift created nxd.db as a side effect")
	}
}

// Wiring test: the projection-drift check must actually run when a user types
// `nxd doctor`. Drive the real command with a config pointing at a drifted
// fixture and assert the drift line appears in the command's output.
func TestDoctorCmd_RunsProjectionDriftCheck(t *testing.T) {
	cfg := seedDriftFixture(t, 6, 1)
	cfgPath := writeDoctorConfig(t, cfg.Workspace.StateDir)

	cmd := newDoctorCmd()
	cmd.Flags().String("config", "", "Path to config file")
	if err := cmd.Flags().Set("config", cfgPath); err != nil {
		t.Fatalf("set config flag: %v", err)
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	_ = cmd.Execute() // may return an error from unrelated checks; we only read output

	output := buf.String()
	if !strings.Contains(output, "Projection drift") {
		t.Fatalf("doctor output missing the Projection drift check:\n%s", output)
	}
	if !strings.Contains(output, "behind") {
		t.Errorf("doctor output should report the seeded drift as behind:\n%s", output)
	}
}
