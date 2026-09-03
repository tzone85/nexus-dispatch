package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// validRemoteDir restricts remote_dir to absolute POSIX paths composed of
// safe characters only. The remote_dir lands inside `cd %s && ...` in
// Run(), so anything that could break out of the cd target (`;`, `&`, `$`,
// backticks, spaces, etc.) must be rejected here, not after the fact.
// Operators with exotic remote layouts can override the regex via
// `extra_flags` plumbed through ssh — this validator covers the shape NXD
// is willing to interpolate without quoting.
var validRemoteDir = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// ValidateRemoteDir reports whether dir is a safe value for SSHRunner.remoteDir.
// Exposed for tests + config-time pre-validation.
func ValidateRemoteDir(dir string) bool {
	if dir == "" || len(dir) > 256 {
		return false
	}
	if strings.Contains(dir, "..") {
		return false
	}
	return validRemoteDir.MatchString(dir)
}

// SSHRunner executes agent sessions on remote machines via SSH.
type SSHRunner struct {
	host       string   // user@host
	keyFile    string   // path to SSH key (optional)
	remoteDir  string   // remote working directory base
	extraFlags []string // additional SSH flags
}

// SSHConfig holds configuration for the SSH runner.
type SSHConfig struct {
	Host       string   `yaml:"host"`       // user@host
	KeyFile    string   `yaml:"key_file"`   // path to private key
	RemoteDir  string   `yaml:"remote_dir"` // remote base directory
	ExtraFlags []string `yaml:"extra_flags"`
}

// NewSSHRunner creates an SSHRunner with the given config.
// Returns an error if remote_dir fails ValidateRemoteDir — operators must
// pick a safe POSIX path so we can interpolate it into remote shell
// commands without escaping every call site.
func NewSSHRunner(cfg SSHConfig) (*SSHRunner, error) {
	remoteDir := cfg.RemoteDir
	if remoteDir == "" {
		remoteDir = "/tmp/nxd-agent"
	}
	if !ValidateRemoteDir(remoteDir) {
		return nil, fmt.Errorf("invalid remote_dir %q: must be an absolute POSIX path of [A-Za-z0-9._/-] characters", remoteDir)
	}
	return &SSHRunner{
		host:       cfg.Host,
		keyFile:    cfg.KeyFile,
		remoteDir:  remoteDir,
		extraFlags: cfg.ExtraFlags,
	}, nil
}

// sshEnvFileRel is the remote-relative path of the staged env file.
const sshEnvFileRel = ".nxd-env.sh"

// Run uploads setup files and starts the execution on the remote machine.
// The environment is shipped as a 0600 env file (scp) that the remote
// command sources and deletes; no secret is ever part of the ssh argv.
func (r *SSHRunner) Run(pe PreparedExecution) error {
	// H13: validate SessionName before using it in remote paths to prevent
	// path traversal on the SSH target (e.g. SessionName="../../etc").
	if !sanitize.ValidIdentifier(pe.SessionName) {
		return fmt.Errorf("invalid session name %q", pe.SessionName)
	}
	remoteWorkDir := filepath.Join(r.remoteDir, pe.SessionName)

	// H14: per-session staging dir so parallel SSH agents do not race on
	// the same /tmp/<basename> path.
	stageDir, err := os.MkdirTemp("", "nxd-ssh-"+pe.SessionName+"-")
	if err != nil {
		return fmt.Errorf("create stage dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	// Setup files keep their path relative to the local WorkDir so the
	// command's relative references (.nxd-prompts/prompt.txt, env.sh) resolve.
	uploads := map[string]string{} // remote relative path -> local staged path
	for localPath, content := range pe.SetupFiles {
		rel := remoteRelPath(pe.WorkDir, localPath)
		staged := filepath.Join(stageDir, filepath.FromSlash(rel))
		if err := writeSecretFile(staged, content); err != nil {
			return fmt.Errorf("write temp file: %w", err)
		}
		uploads[rel] = staged
	}
	if len(pe.Env) > 0 {
		content, err := RenderEnvFile(pe.Env)
		if err != nil {
			return err
		}
		staged := filepath.Join(stageDir, sshEnvFileRel)
		if err := writeSecretFile(staged, content); err != nil {
			return fmt.Errorf("write env file: %w", err)
		}
		uploads[sshEnvFileRel] = staged
	}

	// Create every remote directory in one round trip.
	dirs := map[string]bool{remoteWorkDir: true}
	for rel := range uploads {
		dirs[filepath.Join(remoteWorkDir, filepath.Dir(rel))] = true
	}
	mkdirArgs := []string{"mkdir", "-p"}
	for _, d := range sortedKeys(dirs) {
		mkdirArgs = append(mkdirArgs, d)
	}
	if err := r.sshExec(mkdirArgs...); err != nil {
		return fmt.Errorf("create remote dir: %w", err)
	}

	for _, rel := range sortedKeys(uploads) {
		if err := r.scpTo(uploads[rel], filepath.Join(remoteWorkDir, rel)); err != nil {
			return fmt.Errorf("scp setup file %s: %w", rel, err)
		}
	}

	// Execute command remotely in background (nohup). Every interpolated
	// value is single-quoted via QuoteShellArg.
	prefix := ""
	if len(pe.Env) > 0 {
		prefix = ". ./" + sshEnvFileRel + " && rm -f ./" + sshEnvFileRel + "; "
	}
	remoteCmd := fmt.Sprintf("cd %s && %snohup sh -c %s > /dev/null 2>&1 &",
		remoteWorkDir, prefix, QuoteShellArg(pe.Command))

	if err := r.sshExec("sh", "-c", remoteCmd); err != nil {
		return fmt.Errorf("ssh exec: %w", err)
	}

	return nil
}

// remoteRelPath returns localPath relative to workDir (slash-separated), or
// just its basename when it is not inside workDir.
func remoteRelPath(workDir, localPath string) string {
	if workDir != "" {
		if rel, err := filepath.Rel(workDir, localPath); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(localPath)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Terminate kills the remote process by session ID pattern.
func (r *SSHRunner) Terminate(sessionID string) error {
	cmd := "pkill -f " + QuoteShellArg(sessionID) + " 2>/dev/null || true"
	return r.sshExec("sh", "-c", cmd)
}

// SendInput is not supported for SSH runner.
func (r *SSHRunner) SendInput(sessionID string, input string) error {
	return fmt.Errorf("SendInput not supported for SSH runner")
}

// ReadOutput reads the last N lines from the remote log file.
func (r *SSHRunner) ReadOutput(sessionID string, lines int) (string, error) {
	logPath := filepath.Join(r.remoteDir, sessionID, "agent.log")
	cmd := r.buildSSHCmd("tail", fmt.Sprintf("-%d", lines), logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh tail: %w", err)
	}
	return string(out), nil
}

// IsAlive checks if the remote process is still running.
func (r *SSHRunner) IsAlive(sessionID string) bool {
	cmd := r.buildSSHCmd("pgrep", "-f", sessionID)
	return cmd.Run() == nil
}

// sshExec runs a command on the remote host and returns any error.
func (r *SSHRunner) sshExec(args ...string) error {
	cmd := r.buildSSHCmd(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// buildSSHCmd constructs an SSH command with the runner's config.
func (r *SSHRunner) buildSSHCmd(remoteArgs ...string) *exec.Cmd {
	sshArgs := []string{}
	if r.keyFile != "" {
		sshArgs = append(sshArgs, "-i", r.keyFile)
	}
	sshArgs = append(sshArgs, r.extraFlags...)
	sshArgs = append(sshArgs, r.host)
	sshArgs = append(sshArgs, remoteArgs...)
	return sshExecCommand("ssh", sshArgs...)
}

// scpTo uploads a local file to the remote host.
func (r *SSHRunner) scpTo(localPath, remotePath string) error {
	scpArgs := []string{}
	if r.keyFile != "" {
		scpArgs = append(scpArgs, "-i", r.keyFile)
	}
	scpArgs = append(scpArgs, localPath, r.host+":"+remotePath)
	cmd := sshExecCommand("scp", scpArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sshExecCommand wraps exec.Command for testability (allows mocking in tests).
var sshExecCommand = exec.Command
