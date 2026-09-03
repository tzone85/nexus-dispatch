package criteria

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// command_succeeds and test_passes must run through the installed executor so
// resume.go can route them into the configured sandbox.
func TestSetCommandExecutor_RoutesCommandCriteria(t *testing.T) {
	var got [][]string
	SetCommandExecutor(func(_ context.Context, _ string, argv []string) ([]byte, error) {
		got = append(got, argv)
		if argv[0] == "make" {
			return []byte("make: *** error"), errors.New("exit status 2")
		}
		return []byte("ok"), nil
	})
	t.Cleanup(func() { SetCommandExecutor(nil) })

	dir := t.TempDir()
	r := Evaluate(context.Background(), dir, Criterion{Type: TypeTestPasses, Target: "go test ./internal/..."})
	if !r.Passed {
		t.Errorf("test_passes via executor: %+v", r)
	}
	r = Evaluate(context.Background(), dir, Criterion{Type: TypeCommandSucceeds, Target: "make lint"})
	if r.Passed || r.Actual != "make: *** error" {
		t.Errorf("command_succeeds failure not propagated: %+v", r)
	}
	want := [][]string{{"go", "test", "./internal/..."}, {"make", "lint"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("executor argv = %q, want %q", got, want)
	}
}

func TestSetCommandExecutor_NilRestoresHost(t *testing.T) {
	SetCommandExecutor(func(context.Context, string, []string) ([]byte, error) { return nil, errors.New("fake") })
	SetCommandExecutor(nil)
	out, err := runCriterionCommand(context.Background(), t.TempDir(), []string{"echo", "host"})
	if err != nil || string(out) != "host\n" {
		t.Errorf("host executor not restored: out=%q err=%v", out, err)
	}
}
