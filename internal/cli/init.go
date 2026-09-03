package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// localStateDir is the repo-relative state directory written by --local-state.
const localStateDir = ".nxd"

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the NXD workspace",
		Long: "Generates a default nxd.yaml config, creates the state directory structure and initializes stores.\n\n" +
			"By default state lives in ~/.nxd (shared across repos). With --local-state the generated config\n" +
			"points workspace.state_dir at ./.nxd (per-project) and .nxd/ is added to .gitignore.",
		RunE: runInit,
	}
	cmd.Flags().Bool("local-state", false, "keep state in ./.nxd inside this repo (written to nxd.yaml, added to .gitignore)")
	cmd.SilenceUsage = true
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	localState, _ := cmd.Flags().GetBool("local-state")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	// Generate nxd.yaml from defaults if not present.
	localCfg := "nxd.yaml"
	if _, err := os.Stat(localCfg); os.IsNotExist(err) {
		if err := writeDefaultConfig(out, localCfg, cwd, localState); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "Config %s already exists, skipping\n", localCfg)
	}

	stateDir, mode := initStateDir(localCfg, cwd, localState)

	if localState {
		if err := ensureGitignore(filepath.Join(cwd, ".gitignore"), localStateDir+"/"); err != nil {
			return err
		}
	}

	// Create directory structure.
	dirs := []string{
		stateDir,
		filepath.Join(stateDir, "logs"),
		filepath.Join(stateDir, "worktrees"),
	}
	for _, dir := range dirs {
		// F8: the state dir may hold launch configs, prompts, diffs, agent logs,
		// and (depending on plugins) credential context. 0o700 keeps other
		// local users out without affecting the operator's own access.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Initialize event store.
	eventsPath := filepath.Join(stateDir, "events.jsonl")
	es, err := state.NewFileStore(eventsPath)
	if err != nil {
		return fmt.Errorf("initialize event store: %w", err)
	}
	es.Close()

	// Initialize projection store (SQLite).
	dbPath := filepath.Join(stateDir, "nxd.db")
	ps, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("initialize projection store: %w", err)
	}
	ps.Close()

	fmt.Fprintf(out, "Initialized NXD workspace at %s (state mode: %s)\n", stateDir, mode)
	fmt.Fprintf(out, "  Event store:      %s\n", eventsPath)
	fmt.Fprintf(out, "  Projection store: %s\n", dbPath)

	// Check if Ollama is running (non-blocking, informational only).
	ollamaResult := checkOllamaRunning()
	if ollamaResult.Status != "ok" {
		fmt.Fprintf(out, "\nWarning: Ollama not detected. Install it at https://ollama.com for local LLM inference.\n")
		fmt.Fprintf(out, "  After installing, run: ollama pull gemma4:e4b\n")
	} else {
		fmt.Fprintf(out, "\nOllama detected and running.\n")
	}

	fmt.Fprintf(out, "\nRun 'nxd req \"<requirement>\"' to submit your first requirement.\n")

	return nil
}

// writeDefaultConfig generates nxd.yaml tailored to the project in cwd. With
// localState the config's workspace.state_dir is the repo-relative ".nxd".
func writeDefaultConfig(out io.Writer, path, cwd string, localState bool) error {
	var mutate func(*config.Config)
	if localState {
		mutate = func(c *config.Config) { c.Workspace.StateDir = localStateDir }
	}
	data, label, genErr := config.DefaultYAMLForWith(cwd, mutate)
	if genErr != nil {
		return fmt.Errorf("generate default config: %w", genErr)
	}
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	fmt.Fprintf(out, "Created %s with default configuration (project type: %s)\n", path, label)
	return nil
}

// initStateDir decides where init puts state and reports the mode used:
// "local" when the directory lives inside the repo, "shared" otherwise. The
// existing config wins when it loads (so re-running init in a repo that
// already opted into local state keeps it local); --state-dir wins over both;
// a config that cannot be loaded falls back to the default ~/.nxd.
func initStateDir(cfgPath, cwd string, localState bool) (string, string) {
	var dir string
	switch {
	case stateDirOverride != "":
		dir = config.NormalizeStateDir(stateDirOverride, "")
	case localState:
		dir = filepath.Join(cwd, localStateDir)
	default:
		if cfg, err := loadConfig(cfgPath); err == nil {
			dir = cfg.Workspace.StateDir
		} else {
			dir = config.NormalizeStateDir("", "")
		}
	}
	mode := "shared"
	if rel, err := filepath.Rel(cwd, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		mode = "local"
	}
	return dir, mode
}

// ensureGitignore appends entry to the .gitignore at path unless an
// equivalent line is already present. Creates the file when missing.
func ensureGitignore(path, entry string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	trimmed := strings.TrimSuffix(entry, "/")
	for _, line := range strings.Split(string(existing), "\n") {
		l := strings.TrimSpace(line)
		if l == entry || l == trimmed || l == "/"+entry || l == "/"+trimmed {
			return nil
		}
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("# NXD per-project state (nxd init --local-state)\n" + entry + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	return nil
}
