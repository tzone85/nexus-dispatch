package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
)

// ExecResult is the outcome of a sandboxed command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout followed by stderr, mirroring CombinedOutput for
// callers that feed the text back to a model.
func (r ExecResult) Combined() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

// CommandSandbox executes an argv (no shell) inside workDir. Implementations
// confine the command to a host process (HostSandbox) or a throwaway
// container (DockerSandbox). A non-zero exit is reported via ExitCode with a
// nil error; err is reserved for failures to run the command at all
// (binary missing, docker unavailable, context cancelled).
type CommandSandbox interface {
	Exec(ctx context.Context, workDir string, argv []string, timeout time.Duration) (ExecResult, error)
	Name() string
}

// CommandRunner is the injection point for the process launcher. Production
// uses exec.CommandContext; tests record argv.
type CommandRunner func(ctx context.Context, dir string, name string, args ...string) (stdout, stderr []byte, exitCode int, err error)

// realCommandRunner launches name with args via exec.CommandContext — argv
// only, never a shell.
func realCommandRunner(ctx context.Context, dir string, name string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out.Bytes(), errb.Bytes(), exitErr.ExitCode(), nil
		}
		return out.Bytes(), errb.Bytes(), -1, err
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}

// withTimeout derives a bounded context when timeout > 0.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// ErrSandboxTimeout is wrapped into the error when the command exceeded its
// timeout. Callers can errors.Is against it to phrase feedback for the model.
var ErrSandboxTimeout = errors.New("command timed out")

// ---------------------------------------------------------------------------
// HostSandbox
// ---------------------------------------------------------------------------

// HostSandbox runs the argv directly on the host (exec.CommandContext, no
// shell). This is the pre-sandbox behaviour and the fallback when docker is
// unavailable.
type HostSandbox struct {
	run CommandRunner
}

// NewHostSandbox returns a HostSandbox using the real process launcher.
func NewHostSandbox() *HostSandbox { return &HostSandbox{run: realCommandRunner} }

// Name implements CommandSandbox.
func (h *HostSandbox) Name() string { return "host" }

// Exec implements CommandSandbox.
func (h *HostSandbox) Exec(ctx context.Context, workDir string, argv []string, timeout time.Duration) (ExecResult, error) {
	if len(argv) == 0 {
		return ExecResult{}, errors.New("empty argv")
	}
	cctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	out, errb, code, err := h.run(cctx, workDir, argv[0], argv[1:]...)
	res := ExecResult{Stdout: string(out), Stderr: string(errb), ExitCode: code}
	return res, classifyExecErr(cctx, err)
}

// classifyExecErr maps a deadline-exceeded context into ErrSandboxTimeout.
func classifyExecErr(ctx context.Context, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%w: %v", ErrSandboxTimeout, ctx.Err())
	}
	return err
}

// ---------------------------------------------------------------------------
// DockerSandbox
// ---------------------------------------------------------------------------

// DockerSandbox runs each command in a fresh, locked-down container:
//
//	docker run --rm --network <net> -v <worktree>:/work -w /work
//	  --cpus <n> --memory <m> --cap-drop ALL --security-opt no-new-privileges
//	  [-v <worktree>/<rel>:<dst>[:ro] ...] <image> <argv...>
//
// The argv is passed to the container entrypoint directly — no `sh -c` on the
// host and none inside the container either, so quoting can never be
// re-interpreted.
type DockerSandbox struct {
	Image       string
	Network     string
	CPUs        string
	Memory      string
	ExtraMounts []string
	run         CommandRunner
}

// NewDockerSandbox builds a DockerSandbox from config. Empty fields take the
// defaults from config.DefaultConfig().Sandbox.
func NewDockerSandbox(cfg config.SandboxConfig) *DockerSandbox {
	def := config.DefaultConfig().Sandbox
	pick := func(v, d string) string {
		if strings.TrimSpace(v) == "" {
			return d
		}
		return v
	}
	return &DockerSandbox{
		Image:       pick(cfg.Image, def.Image),
		Network:     pick(cfg.Network, def.Network),
		CPUs:        pick(cfg.CPUs, def.CPUs),
		Memory:      pick(cfg.Memory, def.Memory),
		ExtraMounts: append([]string(nil), cfg.ExtraMounts...),
		run:         realCommandRunner,
	}
}

// WithRunner swaps the process launcher (tests).
func (d *DockerSandbox) WithRunner(r CommandRunner) *DockerSandbox {
	d.run = r
	return d
}

// Name implements CommandSandbox.
func (d *DockerSandbox) Name() string { return "docker" }

// Args returns the exact `docker` argv for running argv in workDir.
// Exposed so tests can assert the flags without launching anything.
func (d *DockerSandbox) Args(workDir string, argv []string) ([]string, error) {
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree: %w", err)
	}
	args := []string{
		"run", "--rm",
		"--network", d.Network,
		"-v", absWork + ":/work",
		"-w", "/work",
		"--cpus", d.CPUs,
		"--memory", d.Memory,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}
	for _, m := range d.ExtraMounts {
		if err := config.ValidateExtraMount(m); err != nil {
			return nil, err
		}
		parts := strings.SplitN(m, ":", 2)
		args = append(args, "-v", filepath.Join(absWork, parts[0])+":"+parts[1])
	}
	args = append(args, d.Image)
	args = append(args, argv...)
	return args, nil
}

// Exec implements CommandSandbox.
func (d *DockerSandbox) Exec(ctx context.Context, workDir string, argv []string, timeout time.Duration) (ExecResult, error) {
	if len(argv) == 0 {
		return ExecResult{}, errors.New("empty argv")
	}
	args, err := d.Args(workDir, argv)
	if err != nil {
		return ExecResult{}, err
	}
	cctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	out, errb, code, err := d.run(cctx, workDir, "docker", args...)
	res := ExecResult{Stdout: string(out), Stderr: string(errb), ExitCode: code}
	return res, classifyExecErr(cctx, err)
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

// DockerProbe reports whether a usable docker daemon is reachable.
type DockerProbe func() bool

// probeDockerOnce caches the result of `docker info` for the process
// lifetime: the probe costs ~100ms and the answer does not change mid-run.
var probeDockerOnce = sync.OnceValue(func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, code, err := realCommandRunner(ctx, "", "docker", "info")
	return err == nil && code == 0
})

// HostFallbackWarning is the one-time message printed when sandbox.mode is
// auto and docker is unavailable.
const HostFallbackWarning = "WARNING: sandbox.mode is auto but docker is unavailable — agent commands " +
	"(run_command, success criteria, investigator) will execute UNSANDBOXED on this host with your user's " +
	"privileges. Install/start docker, or set `sandbox.mode: host` in nxd.yaml to acknowledge the risk and silence this warning."

// NewSandbox selects a CommandSandbox from config.
//
//	docker → DockerSandbox (error if the probe fails)
//	host   → HostSandbox
//	auto   → DockerSandbox when probe() is true, else HostSandbox + warn()
//
// probe may be nil (uses the cached `docker info` probe); warn may be nil
// (logs via the standard logger).
func NewSandbox(cfg config.SandboxConfig, probe DockerProbe, warn func(string)) (CommandSandbox, error) {
	if probe == nil {
		probe = probeDockerOnce
	}
	if warn == nil {
		warn = func(msg string) { log.Print(msg) }
	}
	switch cfg.Mode {
	case "host":
		return NewHostSandbox(), nil
	case "docker":
		if !probe() {
			return nil, errors.New("sandbox.mode is docker but `docker info` failed — start docker or set sandbox.mode: host")
		}
		return NewDockerSandbox(cfg), nil
	case "auto", "":
		if probe() {
			return NewDockerSandbox(cfg), nil
		}
		warn(HostFallbackWarning)
		return NewHostSandbox(), nil
	default:
		return nil, fmt.Errorf("unknown sandbox.mode %q", cfg.Mode)
	}
}

// defaultSandbox is the process-wide sandbox used by runtimes that were not
// given one explicitly (GemmaRuntime.Sandbox == nil). It starts as the host
// sandbox so unit tests and legacy callers keep working; resume.go replaces it
// via SetDefaultSandbox once the config is loaded.
var (
	defaultSandboxMu sync.RWMutex
	defaultSandbox   CommandSandbox = NewHostSandbox()
)

// SetDefaultSandbox installs the process-wide sandbox. nil resets to host.
func SetDefaultSandbox(sb CommandSandbox) {
	defaultSandboxMu.Lock()
	defer defaultSandboxMu.Unlock()
	if sb == nil {
		sb = NewHostSandbox()
	}
	defaultSandbox = sb
}

// DefaultSandbox returns the process-wide sandbox.
func DefaultSandbox() CommandSandbox {
	defaultSandboxMu.RLock()
	defer defaultSandboxMu.RUnlock()
	return defaultSandbox
}

// ArgvExecutor adapts a CommandSandbox to the plain
// func(ctx, workDir, argv) (combinedOutput, error) shape used by
// criteria.SetCommandExecutor. A non-zero exit becomes an error so callers
// that only check err keep their semantics.
func ArgvExecutor(sb CommandSandbox) func(ctx context.Context, workDir string, argv []string) ([]byte, error) {
	return func(ctx context.Context, workDir string, argv []string) ([]byte, error) {
		res, err := sb.Exec(ctx, workDir, argv, 0)
		out := []byte(res.Combined())
		if err != nil {
			return out, err
		}
		if res.ExitCode != 0 {
			return out, fmt.Errorf("exit status %d", res.ExitCode)
		}
		return out, nil
	}
}

// InstallSandbox selects the sandbox from cfg and wires it everywhere native
// tool commands run: the process default used by GemmaRuntime (and the
// investigator via engine), and the criteria evaluator's command_succeeds /
// test_passes executor. The one-line call site lives in cli/resume.go. The
// warning (auto mode, docker unavailable) is written to warn, or stderr when
// warn is nil.
func InstallSandbox(cfg config.SandboxConfig, warn func(string)) (CommandSandbox, error) {
	sb, err := NewSandbox(cfg, nil, warn)
	if err != nil {
		return nil, err
	}
	SetDefaultSandbox(sb)
	criteria.SetCommandExecutor(ArgvExecutor(sb))
	return sb, nil
}
