package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEnvVarName(t *testing.T) {
	for _, ok := range []string{"A", "_X", "OPENAI_API_KEY", "A1_B2"} {
		if err := ValidateEnvVarName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1A", "lower", "A-B", "A B", "A=B", "A;rm", "A\nB", "$A"} {
		if err := ValidateEnvVarName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestRenderEnvFile_QuotesAndSorts(t *testing.T) {
	out, err := RenderEnvFile(map[string]string{
		"ZED":   "plain",
		"ALPHA": "with space",
		"EVIL":  "$(touch /tmp/pwned) `id` \"q\" 'single'",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "#") {
		t.Fatalf("unexpected shape: %q", out)
	}
	if lines[1] != "export ALPHA='with space'" || lines[2] != `export EVIL='$(touch /tmp/pwned) `+"`id`"+` "q" '\''single'\'''` || lines[3] != "export ZED=plain" {
		t.Errorf("lines = %q", lines[1:])
	}
	if _, err := RenderEnvFile(map[string]string{"bad name": "x"}); err == nil {
		t.Error("invalid name must be rejected")
	}
}

// Sourcing the rendered file in a real shell must leave $(...) and backticks
// inert: the value arrives byte-for-byte and no side effect happens.
func TestRenderEnvFile_SubstitutionIsInertWhenSourced(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	val := "$(touch " + marker + ") `touch " + marker + "` it's $HOME"
	content, err := RenderEnvFile(map[string]string{"SECRET": val})
	if err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "env.sh")
	if err := writeSecretFile(envFile, content); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(envFile)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
	out, err := exec.Command("sh", "-c", ". "+envFile+" && printf '%s' \"$SECRET\"").Output()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if string(out) != val {
		t.Errorf("value after sourcing = %q, want %q", out, val)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("command substitution inside the value was executed")
	}
}

func TestRenderDockerEnvFile(t *testing.T) {
	out, err := RenderDockerEnvFile(map[string]string{"B": "2", "A": "x=y"})
	if err != nil || out != "A=x=y\nB=2\n" {
		t.Errorf("out=%q err=%v", out, err)
	}
	if _, err := RenderDockerEnvFile(map[string]string{"A": "multi\nline"}); err == nil {
		t.Error("newline value must be rejected")
	}
	if _, err := RenderDockerEnvFile(map[string]string{"a": "x"}); err == nil {
		t.Error("invalid name must be rejected")
	}
}

func TestSessionEnv_PassthroughAndValidation(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://gpu:11434")
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-leak")
	env, err := sessionEnv(map[string]string{"OLLAMA_HOST": "override", "EXTRA": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if env["OLLAMA_HOST"] != "override" || env["EXTRA"] != "1" {
		t.Errorf("env = %v", env)
	}
	if _, leaked := env["ANTHROPIC_API_KEY"]; leaked {
		t.Error("ANTHROPIC_API_KEY must not be passed through (agents use OAuth)")
	}
	if _, err := sessionEnv(map[string]string{"x": "1"}); err == nil {
		t.Error("lowercase name must be rejected")
	}
}

func TestEnvSourcePrefix(t *testing.T) {
	if got := envSourcePrefix(false); got != "unset CLAUDECODE; " {
		t.Errorf("no env file: %q", got)
	}
	if got := envSourcePrefix(true); got != ". ./.nxd-prompts/env.sh && rm -f ./.nxd-prompts/env.sh; unset CLAUDECODE; " {
		t.Errorf("with env file: %q", got)
	}
}

// The generated tmux command must never carry a secret value and must quote
// every interpolated value with QuoteShellArg (no %q).
func TestPrepareCLIExecution_NoSecretsInCommand(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-$(id)-`whoami`-tok"
	pe, err := prepareCLIExecution("claude", []string{"--verbose"}, SessionConfig{
		WorkDir:     dir,
		Model:       "claude-sonnet-4",
		Goal:        "goal",
		LogFile:     filepath.Join(dir, "my log.txt"),
		SessionName: "s",
		EnvVars:     map[string]string{"API_TOKEN": secret},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pe.Command, secret) || strings.Contains(pe.Command, "API_TOKEN") {
		t.Errorf("secret leaked into command: %s", pe.Command)
	}
	if strings.Contains(pe.Command, `"`+filepath.Join(dir, "my log.txt")+`"`) {
		t.Errorf("log file must be single-quoted via QuoteShellArg, not %%q: %s", pe.Command)
	}
	if !strings.Contains(pe.Command, " 2>&1 | tee '"+filepath.Join(dir, "my log.txt")+"'") {
		t.Errorf("tee target quoting: %s", pe.Command)
	}
	if !strings.Contains(pe.Command, " --model claude-sonnet-4 -p \"$(cat .nxd-prompts/prompt.txt)\"") {
		t.Errorf("model/prompt shape: %s", pe.Command)
	}
	envFile := pe.SetupFiles[filepath.Join(dir, EnvFileRel)]
	if !strings.Contains(envFile, "export API_TOKEN='sk-$(id)-`whoami`-tok'") {
		t.Errorf("env file = %q", envFile)
	}
	if pe.SetupFiles[filepath.Join(dir, PromptFileRel)] != "goal" {
		t.Errorf("prompt file = %q", pe.SetupFiles[filepath.Join(dir, PromptFileRel)])
	}
}

func TestPrepareCLIExecution_NoWorkDirNoEnvFile(t *testing.T) {
	pe, err := prepareCLIExecution("claude", nil, SessionConfig{Goal: "g", EnvVars: map[string]string{"A": "b"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pe.Command, "env.sh") || strings.Contains(pe.Command, "prompt.txt") || len(pe.SetupFiles) != 0 {
		t.Errorf("without a WorkDir nothing can be staged: %+v", pe)
	}
	if !strings.HasPrefix(pe.Command, "unset CLAUDECODE; claude") {
		t.Errorf("command = %s", pe.Command)
	}
}

func TestPrepareCLIExecution_Errors(t *testing.T) {
	if _, err := prepareCLIExecution("claude", []string{"--x;rm"}, SessionConfig{}, false); err == nil {
		t.Error("unsafe arg must be rejected")
	}
	if _, err := prepareCLIExecution("claude", nil, SessionConfig{Model: "m;x"}, false); err == nil {
		t.Error("unsafe model must be rejected")
	}
	if _, err := prepareCLIExecution("claude", nil, SessionConfig{EnvVars: map[string]string{"1X": "v"}}, false); err == nil {
		t.Error("invalid env name must be rejected")
	}
}

func TestPreparedExecution_WriteSetupFiles(t *testing.T) {
	dir := t.TempDir()
	pe := PreparedExecution{SetupFiles: map[string]string{filepath.Join(dir, "a", "b.txt"): "hi"}}
	if err := pe.WriteSetupFiles(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "a", "b.txt"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("setup file mode/err: %v %v", info, err)
	}
	bad := PreparedExecution{SetupFiles: map[string]string{filepath.Join(dir, "a", "b.txt", "c"): "x"}}
	if err := bad.WriteSetupFiles(); err == nil {
		t.Error("writing under a file must fail")
	}
}
