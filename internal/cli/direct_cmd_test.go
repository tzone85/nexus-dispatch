package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

const directReqID = "01TESTREQID01"
const directStoryID = "01TEST-s-001"

func TestDirect_BroadcastsToRequirement(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, directReqID, "Add health check", "/tmp/repo")

	cmd := newDirectCmd()
	out, err := execCmd(t, cmd, env.Config, directReqID, "use", "stdlib", "only")
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	if !strings.Contains(out, "broadcast to requirement") {
		t.Errorf("expected broadcast scope, got: %s", out)
	}
	if !strings.Contains(out, "use stdlib only") {
		t.Errorf("expected instruction echo, got: %s", out)
	}

	events, err := env.Events.List(state.EventFilter{Type: state.EventUserDirective})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 USER_DIRECTIVE event, got %d", len(events))
	}
}

func TestDirect_TargetsStory(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, directReqID, "Add health check", "/tmp/repo")
	seedTestStory(t, env, directStoryID, directReqID, "Setup", 2)

	cmd := newDirectCmd()
	out, err := execCmd(t, cmd, env.Config, directStoryID, "skip the test gate")
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	if !strings.Contains(out, "targeted at story") {
		t.Errorf("expected story scope, got: %s", out)
	}
}

func TestDirect_RejectsUnknownID(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newDirectCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.Flags().String("config", env.Config, "")
	cmd.SetArgs([]string{"01NOTAREALID", "do something"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for unknown id")
	}
}

func TestDirect_RejectsEmptyInstruction(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, directReqID, "Add health check", "/tmp/repo")

	cmd := newDirectCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.Flags().String("config", env.Config, "")
	cmd.SetArgs([]string{directReqID})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for empty instruction")
	}
}
