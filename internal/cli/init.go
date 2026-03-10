package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the NXD workspace",
		Long:  "Creates the ~/.nxd/ directory structure, copies the default config, and initializes stores.",
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

	// Copy example config to nxd.yaml if not present
	localCfg := "nxd.yaml"
	if _, err := os.Stat(localCfg); os.IsNotExist(err) {
		exampleCfg := "nxd.config.example.yaml"
		data, readErr := os.ReadFile(exampleCfg)
		if readErr != nil {
			fmt.Fprintf(out, "Warning: could not read %s, skipping config copy: %v\n", exampleCfg, readErr)
		} else {
			if writeErr := os.WriteFile(localCfg, data, 0644); writeErr != nil {
				return fmt.Errorf("write %s: %w", localCfg, writeErr)
			}
			fmt.Fprintf(out, "Created %s from example config\n", localCfg)
		}
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
	if err := checkOllama(); err != nil {
		fmt.Fprintf(out, "\nWarning: Ollama not detected. Install it at https://ollama.com for local LLM inference.\n")
		fmt.Fprintf(out, "  After installing, run: ollama pull deepseek-coder-v2:latest\n")
	} else {
		fmt.Fprintf(out, "\nOllama detected and running.\n")
	}

	fmt.Fprintf(out, "\nRun 'nxd req \"<requirement>\"' to submit your first requirement.\n")

	return nil
}

// checkOllama performs a quick health check against the local Ollama API.
// Returns nil if Ollama is reachable, or an error otherwise.
func checkOllama() error {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return fmt.Errorf("ollama not reachable: %w", err)
	}
	resp.Body.Close()

	return nil
}
