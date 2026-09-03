package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func eventsPath(env *testEnv) string { return filepath.Join(env.Dir, ".nxd", "events.jsonl") }

func appendRawLine(t *testing.T, path, raw string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(raw); err != nil {
		t.Fatal(err)
	}
}

// runSub executes a grouped command (e.g. `nxd state check`) under a
// throwaway root that carries the persistent --config flag, exactly as the
// real root does.
func runSub(t *testing.T, sub *cobra.Command, cfgPath string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "nxd", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("config", "nxd.yaml", "")
	root.AddCommand(sub)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{sub.Name(), "--config", cfgPath}, args...))
	err := root.Execute()
	return buf.String(), err
}

func runState(t *testing.T, env *testEnv, args ...string) (string, error) {
	t.Helper()
	return runSub(t, newStateCmd(), env.Config, args...)
}

func TestStateCheck_Healthy(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")

	out, err := runState(t, env, "check")
	if err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	for _, want := range []string{"Lines:       1 (1 valid)", "healthy", eventsPath(env)} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestStateCheck_UnhealthyReturnsErrorAndDetails(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")
	appendRawLine(t, eventsPath(env), "{bad-line\n")
	appendRawLine(t, eventsPath(env), `{"torn":`)

	out, err := runState(t, env, "check")
	if err == nil {
		t.Fatal("check must fail on an unhealthy log")
	}
	for _, want := range []string{"Torn tail:   yes", "Malformed:   1 line(s)", "line 2:", "NEEDS REPAIR"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestStateCheck_JSON(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")
	appendRawLine(t, eventsPath(env), "{bad\n")

	out, err := runState(t, env, "check", "--json")
	if err != nil {
		t.Fatalf("--json check should not fail even when unhealthy (report is the output): %v", err)
	}
	var rep state.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.Valid != 1 || len(rep.Malformed) != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestStateCheck_MissingLog(t *testing.T) {
	env := setupTestEnv(t)
	env.Events.Close()
	os.Remove(eventsPath(env))
	if _, err := runState(t, env, "check"); err == nil {
		t.Fatal("expected error for missing events.jsonl")
	}
}

func TestStateCheck_BadConfig(t *testing.T) {
	if _, err := runSub(t, newStateCmd(), filepath.Join(t.TempDir(), "nope.yaml"), "check"); err == nil {
		t.Fatal("expected config error")
	}
}

func TestStateRepair_MovesAndReports(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")
	appendRawLine(t, eventsPath(env), "{bad\n")

	out, err := runState(t, env, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	if !strings.Contains(out, "moved 1 line(s)") || !strings.Contains(out, "events.quarantine.jsonl") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if _, err := os.Stat(eventsPath(env) + ".bak"); err != nil {
		t.Errorf("backup missing: %v", err)
	}
	// Second run: nothing to do.
	out, err = runState(t, env, "repair")
	if err != nil || !strings.Contains(out, "nothing to repair") {
		t.Errorf("second repair: %v\n%s", err, out)
	}
}

func TestStateRepair_JSON(t *testing.T) {
	env := setupTestEnv(t)
	appendRawLine(t, eventsPath(env), "{bad\n")
	out, err := runState(t, env, "repair", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res["moved"] != float64(1) {
		t.Errorf("moved = %v", res["moved"])
	}
}

func TestStateRepair_RefusesWhilePipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	appendRawLine(t, eventsPath(env), "{bad\n")
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	_, err = runState(t, env, "repair")
	if err == nil || !strings.Contains(err.Error(), "pipeline is active") {
		t.Fatalf("expected lock refusal, got %v", err)
	}
	raw, _ := os.ReadFile(eventsPath(env))
	if !strings.Contains(string(raw), "{bad") {
		t.Error("log must be untouched when repair is refused")
	}
}

func TestStateRepair_MissingLog(t *testing.T) {
	env := setupTestEnv(t)
	env.Events.Close()
	os.Remove(eventsPath(env))
	if _, err := runState(t, env, "repair"); err == nil {
		t.Fatal("expected error")
	}
}

func seedCompletedReqWithProgress(t *testing.T, env *testEnv) {
	t.Helper()
	seedTestReq(t, env, "r1", "Done", "/repo")
	seedTestStory(t, env, "s1", "r1", "Story", 2)
	for _, evt := range []state.Event{
		state.NewEvent(state.EventStoryProgress, "a1", "s1", map[string]any{"msg": "x"}),
		state.NewEvent(state.EventAgentCheckpoint, "a1", "s1", map[string]any{"i": 1}),
		state.NewEvent(state.EventReqCompleted, "system", "", map[string]any{"id": "r1"}),
	} {
		env.Events.Append(evt)
		env.Proj.Project(evt)
	}
}

func TestStateCompact_ArchivesAndReports(t *testing.T) {
	env := setupTestEnv(t)
	seedCompletedReqWithProgress(t, env)

	out, err := runState(t, env, "compact")
	if err != nil {
		t.Fatalf("compact: %v\n%s", err, out)
	}
	if !strings.Contains(out, "removed 2 events, kept 3") || !strings.Contains(out, "events.archive-") {
		t.Errorf("unexpected output:\n%s", out)
	}
	// Projection still reflects the requirement after a rebuild of the compacted log.
	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Proj.RebuildFrom(t.Context(), s.Events); err != nil {
		t.Fatal(err)
	}
	req, err := s.Proj.GetRequirement("r1")
	if err != nil || req.Status != "completed" {
		t.Errorf("req after compact+rebuild = %+v, %v", req, err)
	}
}

func TestStateCompact_NothingToDo(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")
	out, err := runState(t, env, "compact")
	if err != nil || !strings.Contains(out, "Nothing to compact") {
		t.Errorf("compact: %v\n%s", err, out)
	}
}

func TestStateCompact_JSON(t *testing.T) {
	env := setupTestEnv(t)
	seedCompletedReqWithProgress(t, env)
	out, err := runState(t, env, "compact", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res state.CompactResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res.Removed != 2 {
		t.Errorf("Removed = %d", res.Removed)
	}
}

func TestStateCompact_RefusesUnhealthy(t *testing.T) {
	env := setupTestEnv(t)
	appendRawLine(t, eventsPath(env), "{bad\n")
	if _, err := runState(t, env, "compact"); err == nil || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("expected refusal pointing at repair, got %v", err)
	}
}

func TestStateCompact_RefusesWhilePipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, err := runState(t, env, "compact"); err == nil || !strings.Contains(err.Error(), "pipeline is active") {
		t.Fatalf("expected lock refusal, got %v", err)
	}
}

func TestStateCompact_BadConfig(t *testing.T) {
	if _, err := runSub(t, newStateCmd(), filepath.Join(t.TempDir(), "nope.yaml"), "compact"); err == nil {
		t.Fatal("expected config error")
	}
}

func TestStateRebuild_ReplaysLog(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 4)

	out, err := runState(t, env, "rebuild")
	if err != nil {
		t.Fatalf("rebuild: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Rebuilt projection from 4 events") {
		t.Errorf("unexpected output:\n%s", out)
	}
	applied, _ := env.Proj.AppliedEventCount()
	if applied != 4 {
		t.Errorf("watermark = %d, want 4", applied)
	}
}

func TestStateRebuild_JSON(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Title", "/repo")
	out, err := runState(t, env, "rebuild", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res["applied_events"] != float64(1) {
		t.Errorf("applied_events = %v", res["applied_events"])
	}
}

func TestStateRebuild_RefusesWhilePipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, err := runState(t, env, "rebuild"); err == nil || !strings.Contains(err.Error(), "lock held") {
		t.Fatalf("expected lock refusal, got %v", err)
	}
}

func TestStateRebuild_BadConfig(t *testing.T) {
	if _, err := runSub(t, newStateCmd(), filepath.Join(t.TempDir(), "nope.yaml"), "rebuild"); err == nil {
		t.Fatal("expected config error")
	}
}

func TestStateRebuild_CorruptLogFails(t *testing.T) {
	env := setupTestEnv(t)
	appendRawLine(t, eventsPath(env), "{bad\n")
	if _, err := runState(t, env, "rebuild"); err == nil {
		t.Fatal("expected error from corrupt log")
	}
}

func TestStateCmd_HasAllSubcommands(t *testing.T) {
	cmd := newStateCmd()
	want := map[string]bool{"check": false, "repair": false, "compact": false, "rebuild": false}
	for _, c := range cmd.Commands() {
		want[c.Name()] = true
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("missing subcommand %s", name)
		}
	}
	if cmd.PersistentFlags().Lookup("json") == nil {
		t.Error("--json flag missing")
	}
}
