package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// fakeRunner records every call so tests can assert the registry delegates.
type fakeRunner struct {
	runs       []PreparedExecution
	terminated []string
	inputs     []string
	readCalls  []string
	output     string
	alive      bool
	runErr     error
}

func (f *fakeRunner) Run(pe PreparedExecution) error {
	f.runs = append(f.runs, pe)
	return f.runErr
}
func (f *fakeRunner) Terminate(id string) error { f.terminated = append(f.terminated, id); return nil }
func (f *fakeRunner) SendInput(id, in string) error {
	f.inputs = append(f.inputs, id+":"+in)
	return nil
}
func (f *fakeRunner) ReadOutput(id string, _ int) (string, error) {
	f.readCalls = append(f.readCalls, id)
	return f.output, nil
}
func (f *fakeRunner) IsAlive(string) bool { return f.alive }

// swapRunnerFactory makes NewRegistry hand out the fake for every runtime and
// records which RuntimeConfig it was asked about.
func swapRunnerFactory(t *testing.T, fr Runner) *[]config.RuntimeConfig {
	t.Helper()
	var seen []config.RuntimeConfig
	orig := newRunnerFromConfig
	newRunnerFromConfig = func(rc config.RuntimeConfig) (Runner, error) {
		seen = append(seen, rc)
		return fr, nil
	}
	t.Cleanup(func() { newRunnerFromConfig = orig })
	return &seen
}

func TestNewRegistry_SelectsRunnerFromConfig(t *testing.T) {
	fr := &fakeRunner{}
	seen := swapRunnerFactory(t, fr)
	reg, err := NewRegistry(map[string]config.RuntimeConfig{
		"dockerized": {Command: "claude", Runner: "docker", Docker: config.DockerRunnerConfig{Image: "img"}},
		"gemma":      {Native: true},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0].Runner != "docker" {
		t.Fatalf("factory must be consulted once per CLI runtime with its config, got %+v", *seen)
	}
	rt, _ := reg.Get("dockerized")
	if rt.(*CLIRuntime).Runner() != fr {
		t.Error("runtime must hold the runner returned by the factory")
	}
}

func TestNewRegistry_RunnerFactoryErrorPropagates(t *testing.T) {
	_, err := NewRegistry(map[string]config.RuntimeConfig{
		"bad": {Command: "claude", Runner: "docker"}, // docker without image
	})
	if err == nil || !strings.Contains(err.Error(), "docker runner requires") {
		t.Fatalf("err = %v, want docker image error", err)
	}
	_, err = NewRegistry(map[string]config.RuntimeConfig{"weird": {Command: "x", Runner: "vm"}})
	if err == nil || !strings.Contains(err.Error(), "unknown runner type") {
		t.Fatalf("err = %v, want unknown runner error", err)
	}
}

func TestNewRegistry_DefaultRunnerIsTmux(t *testing.T) {
	reg, err := NewRegistry(map[string]config.RuntimeConfig{"plain": {Command: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := reg.Get("plain")
	if _, ok := rt.(*CLIRuntime).Runner().(*TmuxRunner); !ok {
		t.Errorf("default runner = %T, want *TmuxRunner", rt.(*CLIRuntime).Runner())
	}
}

func TestCLIRuntime_DelegatesToRunner(t *testing.T) {
	fr := &fakeRunner{output: "> done", alive: true}
	swapRunnerFactory(t, fr)
	reg, err := NewRegistry(map[string]config.RuntimeConfig{
		"rt": {
			Command:   "claude",
			Args:      []string{"--verbose"},
			Runner:    "ssh",
			SSH:       config.SSHRunnerConfig{Host: "u@h"},
			Detection: config.RuntimeDetection{IdlePattern: `^>`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := reg.Get("rt")
	dir := t.TempDir()

	if err := rt.Spawn(SessionConfig{WorkDir: dir, SessionName: "s1", Goal: "do it", Model: "opus-4", EnvVars: map[string]string{"TOK": "v"}}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(fr.runs) != 1 {
		t.Fatalf("Run called %d times, want 1", len(fr.runs))
	}
	pe := fr.runs[0]
	if pe.SessionName != "s1" || pe.WorkDir != dir || pe.Env["TOK"] != "v" {
		t.Errorf("PreparedExecution = %+v", pe)
	}
	if !strings.Contains(pe.Command, "claude --verbose --dangerously-skip-permissions --model opus-4") {
		t.Errorf("command must use EffectiveArgs (ssh runner ⇒ unattended flag): %s", pe.Command)
	}
	for _, want := range []string{filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, PromptFileRel), filepath.Join(dir, EnvFileRel)} {
		if _, ok := pe.SetupFiles[want]; !ok {
			t.Errorf("SetupFiles missing %s: %v", want, pe.SetupFiles)
		}
	}
	// Spawn must not touch the filesystem itself — the runner owns I/O.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("Spawn wrote CLAUDE.md directly instead of delegating to the runner")
	}

	if err := rt.Terminate("s1"); err != nil || len(fr.terminated) != 1 || fr.terminated[0] != "s1" {
		t.Errorf("Terminate not delegated: %v %v", err, fr.terminated)
	}
	if err := rt.SendInput("s1", "Y"); err != nil || fr.inputs[0] != "s1:Y" {
		t.Errorf("SendInput not delegated: %v %v", err, fr.inputs)
	}
	out, err := rt.ReadOutput("s1", 5)
	if err != nil || out != "> done" || fr.readCalls[0] != "s1" {
		t.Errorf("ReadOutput not delegated: %q %v %v", out, err, fr.readCalls)
	}
	st, err := rt.DetectStatus("s1")
	if err != nil || st != StatusDone {
		t.Errorf("DetectStatus = %v, %v; want done via runner output", st, err)
	}
}

func TestCLIRuntime_SpawnReturnsRunnerError(t *testing.T) {
	fr := &fakeRunner{runErr: errors.New("docker down")}
	swapRunnerFactory(t, fr)
	reg, _ := NewRegistry(map[string]config.RuntimeConfig{"rt": {Command: "claude"}})
	rt, _ := reg.Get("rt")
	if err := rt.Spawn(SessionConfig{WorkDir: t.TempDir(), SessionName: "s"}); err == nil || !strings.Contains(err.Error(), "docker down") {
		t.Errorf("err = %v", err)
	}
	if err := rt.Spawn(SessionConfig{WorkDir: t.TempDir(), EnvVars: map[string]string{"bad-name": "x"}}); err == nil {
		t.Error("invalid env var name must fail Spawn before reaching the runner")
	}
}

func TestCLIRuntime_HostRunnerKeepsPermissionPrompts(t *testing.T) {
	fr := &fakeRunner{}
	swapRunnerFactory(t, fr)
	reg, _ := NewRegistry(map[string]config.RuntimeConfig{"cc": {Command: "claude"}})
	rt, _ := reg.Get("cc")
	_ = rt.Spawn(SessionConfig{WorkDir: t.TempDir(), SessionName: "s"})
	if strings.Contains(fr.runs[0].Command, "--dangerously-skip-permissions") {
		t.Errorf("host (tmux) runtime must not get the unattended flag: %s", fr.runs[0].Command)
	}
}

func TestCLIRuntime_WithRunner(t *testing.T) {
	rt := &CLIRuntime{name: "x"}
	fr := &fakeRunner{}
	if rt.WithRunner(fr).Runner() != fr {
		t.Error("WithRunner must install the runner")
	}
}
