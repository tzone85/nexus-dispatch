package tmux

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ClearStaleCriticalEnv must only ever UNSET variables in the tmux global
// environment: no value may travel through tmux argv.
func TestClearStaleCriticalEnv_UnsetsOnlyNeverSetsValues(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-value")
	var calls [][]string
	stop := SetTestExec(func(args ...string) error {
		calls = append(calls, args)
		return errors.New("no server running") // ignored
	}, nil)
	defer stop()

	ClearStaleCriticalEnv()

	want := [][]string{
		{"set-environment", "-g", "-u", "ANTHROPIC_API_KEY"},
		{"set-environment", "-g", "-u", "OPENAI_API_KEY"},
		{"set-environment", "-g", "-u", "OLLAMA_HOST"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("tmux calls = %q, want %q", calls, want)
	}
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "sk-secret-value") {
			t.Errorf("secret value leaked into tmux argv: %q", c)
		}
	}
}

func TestClearStaleEnv_Empty(t *testing.T) {
	called := false
	stop := SetTestExec(func(args ...string) error { called = true; return nil }, nil)
	defer stop()
	ClearStaleEnv(nil)
	if called {
		t.Error("no vars ⇒ no tmux calls")
	}
}
