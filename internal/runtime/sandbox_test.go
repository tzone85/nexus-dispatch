package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
)

// recordingRunner captures every launch and replays canned results.
type recordingRunner struct {
	dir    string
	name   string
	args   []string
	stdout string
	stderr string
	code   int
	err    error
}

func (r *recordingRunner) run(_ context.Context, dir, name string, args ...string) ([]byte, []byte, int, error) {
	r.dir, r.name, r.args = dir, name, args
	return []byte(r.stdout), []byte(r.stderr), r.code, r.err
}

func TestHostSandbox_RealEcho(t *testing.T) {
	sb := NewHostSandbox()
	if sb.Name() != "host" {
		t.Fatalf("Name = %q", sb.Name())
	}
	res, err := sb.Exec(context.Background(), t.TempDir(), []string{"echo", "hello from sandbox"}, 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "hello from sandbox" || res.Stderr != "" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestHostSandbox_RunsInWorkDir(t *testing.T) {
	dir := t.TempDir()
	res, err := NewHostSandbox().Exec(context.Background(), dir, []string{"pwd"}, 0)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(res.Stdout))
	if got != want {
		t.Errorf("pwd = %q, want %q", got, want)
	}
}

func TestHostSandbox_NonZeroExitIsNotAnError(t *testing.T) {
	res, err := NewHostSandbox().Exec(context.Background(), t.TempDir(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, 0)
	if err != nil {
		t.Fatalf("non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode != 3 || strings.TrimSpace(res.Stdout) != "out" || strings.TrimSpace(res.Stderr) != "err" {
		t.Errorf("result = %+v", res)
	}
	if res.Combined() != "out\n\nerr\n" {
		t.Errorf("Combined = %q", res.Combined())
	}
}

func TestExecResult_Combined(t *testing.T) {
	if got := (ExecResult{Stdout: "a"}).Combined(); got != "a" {
		t.Errorf("stdout only = %q", got)
	}
	if got := (ExecResult{Stderr: "b"}).Combined(); got != "b" {
		t.Errorf("stderr only = %q", got)
	}
}

func TestHostSandbox_MissingBinaryIsError(t *testing.T) {
	_, err := NewHostSandbox().Exec(context.Background(), t.TempDir(), []string{"nxd-definitely-not-a-binary-xyz"}, 0)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestHostSandbox_EmptyArgv(t *testing.T) {
	if _, err := NewHostSandbox().Exec(context.Background(), t.TempDir(), nil, 0); err == nil {
		t.Fatal("expected error for empty argv")
	}
	if _, err := NewDockerSandbox(config.SandboxConfig{}).Exec(context.Background(), t.TempDir(), nil, 0); err == nil {
		t.Fatal("expected error for empty argv (docker)")
	}
}

func TestHostSandbox_Timeout(t *testing.T) {
	_, err := NewHostSandbox().Exec(context.Background(), t.TempDir(), []string{"sleep", "5"}, 50*time.Millisecond)
	if !errors.Is(err, ErrSandboxTimeout) {
		t.Fatalf("err = %v, want ErrSandboxTimeout", err)
	}
}

func TestDockerSandbox_ArgsExactFlags(t *testing.T) {
	work := t.TempDir()
	absWork, _ := filepath.Abs(work)
	d := NewDockerSandbox(config.SandboxConfig{
		Image:       "golang:1.26-alpine",
		Network:     "none",
		CPUs:        "1.5",
		Memory:      "512m",
		ExtraMounts: []string{"vendor:/vendor:ro"},
	})
	rr := &recordingRunner{stdout: "ok\n"}
	d.WithRunner(rr.run)
	if d.Name() != "docker" {
		t.Fatalf("Name = %q", d.Name())
	}

	res, err := d.Exec(context.Background(), work, []string{"go", "test", "./..."}, time.Minute)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "ok\n" || res.ExitCode != 0 {
		t.Errorf("result = %+v", res)
	}
	if rr.name != "docker" {
		t.Errorf("launched %q, want docker", rr.name)
	}
	want := []string{
		"run", "--rm",
		"--network", "none",
		"-v", absWork + ":/work",
		"-w", "/work",
		"--cpus", "1.5",
		"--memory", "512m",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"-v", filepath.Join(absWork, "vendor") + ":/vendor:ro",
		"golang:1.26-alpine",
		"go", "test", "./...",
	}
	if !reflect.DeepEqual(rr.args, want) {
		t.Errorf("docker argv mismatch\n got: %q\nwant: %q", rr.args, want)
	}
	// No shell anywhere in the argv.
	for _, a := range rr.args {
		if a == "sh" || a == "-c" || strings.Contains(a, "sh -c") {
			t.Errorf("argv must not invoke a shell: %q", rr.args)
		}
	}
}

func TestDockerSandbox_DefaultsFromConfig(t *testing.T) {
	d := NewDockerSandbox(config.SandboxConfig{})
	def := config.DefaultConfig().Sandbox
	if d.Image != def.Image || d.Network != def.Network || d.CPUs != def.CPUs || d.Memory != def.Memory {
		t.Errorf("defaults not applied: %+v", d)
	}
}

func TestDockerSandbox_RejectsBadExtraMount(t *testing.T) {
	d := NewDockerSandbox(config.SandboxConfig{ExtraMounts: []string{"../secrets:/s"}})
	rr := &recordingRunner{}
	d.WithRunner(rr.run)
	if _, err := d.Exec(context.Background(), t.TempDir(), []string{"go", "build"}, 0); err == nil {
		t.Fatal("expected mount validation error")
	}
	if rr.name != "" {
		t.Error("docker must not be launched when a mount is invalid")
	}
}

func TestDockerSandbox_PropagatesExitAndError(t *testing.T) {
	d := NewDockerSandbox(config.SandboxConfig{})
	rr := &recordingRunner{stderr: "boom", code: 2}
	d.WithRunner(rr.run)
	res, err := d.Exec(context.Background(), t.TempDir(), []string{"make"}, 0)
	if err != nil || res.ExitCode != 2 || res.Stderr != "boom" {
		t.Errorf("res=%+v err=%v", res, err)
	}
	rr.err = errors.New("docker daemon gone")
	if _, err := d.Exec(context.Background(), t.TempDir(), []string{"make"}, 0); err == nil || !strings.Contains(err.Error(), "daemon gone") {
		t.Errorf("err = %v", err)
	}
}

func TestDockerSandbox_TimeoutClassified(t *testing.T) {
	d := NewDockerSandbox(config.SandboxConfig{})
	d.WithRunner(func(ctx context.Context, _, _ string, _ ...string) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	})
	_, err := d.Exec(context.Background(), t.TempDir(), []string{"make"}, 10*time.Millisecond)
	if !errors.Is(err, ErrSandboxTimeout) {
		t.Errorf("err = %v, want ErrSandboxTimeout", err)
	}
}

func TestNewSandbox_Selection(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	var warned []string
	warn := func(m string) { warned = append(warned, m) }

	cases := []struct {
		name     string
		mode     string
		probe    DockerProbe
		wantName string
		wantErr  bool
		wantWarn bool
	}{
		{"host ignores probe", "host", no, "host", false, false},
		{"docker with daemon", "docker", yes, "docker", false, false},
		{"docker without daemon", "docker", no, "", true, false},
		{"auto with daemon", "auto", yes, "docker", false, false},
		{"auto without daemon warns", "auto", no, "host", false, true},
		{"empty mode is auto", "", no, "host", false, true},
		{"unknown mode", "vm", yes, "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warned = nil
			sb, err := NewSandbox(config.SandboxConfig{Mode: tc.mode, Image: "img"}, tc.probe, warn)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && sb.Name() != tc.wantName {
				t.Errorf("sandbox = %s, want %s", sb.Name(), tc.wantName)
			}
			if got := len(warned) == 1; got != tc.wantWarn {
				t.Errorf("warned=%v, want %v", warned, tc.wantWarn)
			}
			if tc.wantWarn && (!strings.Contains(warned[0], "UNSANDBOXED") || !strings.Contains(warned[0], "sandbox.mode: host")) {
				t.Errorf("warning must name the risk and the override: %q", warned[0])
			}
		})
	}
}

func TestNewSandbox_NilWarnUsesLogger(t *testing.T) {
	sb, err := NewSandbox(config.SandboxConfig{Mode: "auto"}, func() bool { return false }, nil)
	if err != nil || sb.Name() != "host" {
		t.Fatalf("sb=%v err=%v", sb, err)
	}
}

func TestNewSandbox_NilProbeUsesCachedDockerInfo(t *testing.T) {
	// The real probe may or may not find docker; either outcome is valid,
	// but the call must not error in host mode and must be stable.
	first := probeDockerOnce()
	second := probeDockerOnce()
	if first != second {
		t.Error("probe result must be cached")
	}
	sb, err := NewSandbox(config.SandboxConfig{Mode: "auto"}, nil, func(string) {})
	if err != nil {
		t.Fatalf("auto with real probe: %v", err)
	}
	if (sb.Name() == "docker") != first {
		t.Errorf("selection %s inconsistent with probe %v", sb.Name(), first)
	}
}

func TestDefaultSandbox_SetAndReset(t *testing.T) {
	orig := DefaultSandbox()
	t.Cleanup(func() { SetDefaultSandbox(orig) })
	if orig.Name() != "host" {
		t.Fatalf("initial default = %s, want host", orig.Name())
	}
	d := NewDockerSandbox(config.SandboxConfig{})
	SetDefaultSandbox(d)
	if DefaultSandbox() != d {
		t.Error("SetDefaultSandbox did not install the sandbox")
	}
	SetDefaultSandbox(nil)
	if DefaultSandbox().Name() != "host" {
		t.Error("nil must reset to host")
	}
}

func TestArgvExecutor(t *testing.T) {
	exec := ArgvExecutor(NewHostSandbox())
	out, err := exec(context.Background(), t.TempDir(), []string{"echo", "hi"})
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("out=%q err=%v", out, err)
	}
	out, err = exec(context.Background(), t.TempDir(), []string{"sh", "-c", "echo fail; exit 4"})
	if err == nil || !strings.Contains(err.Error(), "exit status 4") || !strings.Contains(string(out), "fail") {
		t.Errorf("out=%q err=%v", out, err)
	}
	if _, err := exec(context.Background(), t.TempDir(), []string{"nxd-missing-binary-xyz"}); err == nil {
		t.Error("missing binary must surface as error")
	}
}

// The gemma runtime routes run_command through its Sandbox field.
func TestExecRunCommand_UsesInjectedSandbox(t *testing.T) {
	rr := &recordingRunner{stdout: "sandboxed ok"}
	d := NewDockerSandbox(config.SandboxConfig{Image: "img"}).WithRunner(rr.run)
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 1, CommandAllowlist: []string{"go test"}})
	rt.Sandbox = d
	work := t.TempDir()
	res := rt.execRunCommand(context.Background(), makeToolCall("run_command", map[string]string{"command": "go test ./..."}), work)
	if res.IsError || res.Content != "sandboxed ok" {
		t.Fatalf("result = %+v", res)
	}
	if rr.name != "docker" || rr.args[len(rr.args)-3] != "go" {
		t.Errorf("command did not go through docker sandbox: %s %q", rr.name, rr.args)
	}
	// Non-zero exit surfaces as an error with output.
	rr.code = 1
	rr.stderr = "FAIL pkg"
	res = rt.execRunCommand(context.Background(), makeToolCall("run_command", map[string]string{"command": "go test ./..."}), work)
	if !res.IsError || !strings.Contains(res.Content, "exit status 1") || !strings.Contains(res.Content, "FAIL pkg") {
		t.Errorf("result = %+v", res)
	}
}

func TestExecRunCommand_TimeoutMessage(t *testing.T) {
	d := NewDockerSandbox(config.SandboxConfig{}).WithRunner(func(ctx context.Context, _, _ string, _ ...string) ([]byte, []byte, int, error) {
		return nil, nil, -1, context.DeadlineExceeded
	})
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 1, CommandAllowlist: []string{"go test"}})
	rt.Sandbox = d
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	res := rt.execRunCommand(ctx, makeToolCall("run_command", map[string]string{"command": "go test ./..."}), t.TempDir())
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("result = %+v", res)
	}
}

func TestExecRunCommand_RejectsWorktreeEscape(t *testing.T) {
	rt := newTestRuntime(t) // allowlist: echo, ls, cat
	res := rt.execRunCommand(context.Background(), makeToolCall("run_command", map[string]string{"command": "cat /etc/passwd"}), t.TempDir())
	if !res.IsError || !strings.Contains(res.Content, "allowlist") {
		t.Errorf("result = %+v", res)
	}
	for cmd, hint := range map[string]string{
		"mkdir x":          "write_file",
		"rm -rf x":         "file mutation",
		"git status":       "do NOT run git",
		"echo a && echo b": "chained commands",
	} {
		res := rt.execRunCommand(context.Background(), makeToolCall("run_command", map[string]string{"command": cmd}), t.TempDir())
		if !res.IsError || !strings.Contains(res.Content, hint) {
			t.Errorf("%q: result = %+v, want hint %q", cmd, res, hint)
		}
	}
}

func TestInstallSandbox_WiresDefaultAndCriteria(t *testing.T) {
	orig := DefaultSandbox()
	t.Cleanup(func() {
		SetDefaultSandbox(orig)
		criteria.SetCommandExecutor(nil)
	})
	var warned string
	sb, err := InstallSandbox(config.SandboxConfig{Mode: "host"}, func(m string) { warned = m })
	if err != nil || sb.Name() != "host" || warned != "" {
		t.Fatalf("sb=%v err=%v warned=%q", sb, err, warned)
	}
	if DefaultSandbox() != sb {
		t.Error("InstallSandbox must install the process default")
	}
	// Criteria now run through the sandbox: a real echo passes command_succeeds
	// only if it is allowlisted there, so use test_passes' argv path instead
	// by checking the executor directly through EvaluateAll with a failing go
	// test target that cannot resolve — the executor is exercised either way.
	r := criteria.Evaluate(context.Background(), t.TempDir(), criteria.Criterion{Type: criteria.TypeCommandSucceeds, Target: "git status"})
	if r.Passed {
		t.Errorf("git status in an empty temp dir must fail through the sandbox executor: %+v", r)
	}
	if _, err := InstallSandbox(config.SandboxConfig{Mode: "docker"}, nil); err == nil && !probeDockerOnce() {
		t.Error("docker mode without docker must error")
	}
}
