package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the NXD workspace",
		Long:  "Creates the ~/.nxd/ directory structure, generates a default nxd.yaml config, and initializes stores.",
		RunE:  runInit,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determine home directory: %w", err)
	}

	nxdDir := filepath.Join(home, ".nxd")

	// Create directory structure
	dirs := []string{
		nxdDir,
		filepath.Join(nxdDir, "logs"),
		filepath.Join(nxdDir, "worktrees"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	out := cmd.OutOrStdout()

	// Generate nxd.yaml from defaults if not present
	localCfg := "nxd.yaml"
	if _, err := os.Stat(localCfg); os.IsNotExist(err) {
		data, genErr := config.DefaultYAML()
		if genErr != nil {
			return fmt.Errorf("generate default config: %w", genErr)
		}
		if writeErr := os.WriteFile(localCfg, data, 0644); writeErr != nil {
			return fmt.Errorf("write %s: %w", localCfg, writeErr)
		}
		fmt.Fprintf(out, "Created %s with default configuration\n", localCfg)
	} else {
		fmt.Fprintf(out, "Config %s already exists, skipping\n", localCfg)
	}

	// Initialize event store
	eventsPath := filepath.Join(nxdDir, "events.jsonl")
	es, err := state.NewFileStore(eventsPath)
	if err != nil {
		return fmt.Errorf("initialize event store: %w", err)
	}
	es.Close()

	// Initialize projection store (SQLite)
	dbPath := filepath.Join(nxdDir, "nxd.db")
	ps, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("initialize projection store: %w", err)
	}
	ps.Close()

	fmt.Fprintf(out, "Initialized NXD workspace at %s\n", nxdDir)
	fmt.Fprintf(out, "  Event store:      %s\n", eventsPath)
	fmt.Fprintf(out, "  Projection store: %s\n", dbPath)

	// Check if Ollama is running (non-blocking, informational only)
	ollamaResult := checkOllamaRunning()
	if ollamaResult.Status != "ok" {
		fmt.Fprintf(out, "\nWarning: Ollama not detected. Install it at https://ollama.com for local LLM inference.\n")
		fmt.Fprintf(out, "  After installing, run: ollama pull deepseek-coder-v2:latest\n")
	} else {
		fmt.Fprintf(out, "\nOllama detected and running.\n")
	}

	fmt.Fprintf(out, "\nRun 'nxd req \"<requirement>\"' to submit your first requirement.\n")

	return nil
}

