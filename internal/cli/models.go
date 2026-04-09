package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage and check model versions",
	}
	cmd.AddCommand(newModelsCheckCmd())
	return cmd
}

func newModelsCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check for model updates from Ollama and Google AI Studio",
		Long:  "Queries Ollama registry and Google AI Studio for newer versions of configured models. Always runs even if update_check is disabled in config.",
		RunE:  runModelsCheck,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runModelsCheck(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	cachePath := filepath.Join(stateDir, "update-status.json")
	out := cmd.OutOrStdout()

	ollamaModels, googleModels := collectConfiguredModels(cfg)

	opts := []update.CheckerOption{}
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		opts = append(opts, update.WithOllamaLocalURL(host))
	}
	if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
		opts = append(opts, update.WithGoogleAPIKey(key))
	}
	checker := update.NewChecker(opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checker.RunCheck(ctx, ollamaModels, googleModels)

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		fmt.Fprintf(out, "Warning: could not create state dir: %v\n", err)
	}
	if err := update.WriteCache(cachePath, result); err != nil {
		fmt.Fprintf(out, "Warning: could not write cache: %v\n", err)
	}

	update.PrintReport(out, result, cfg.Workspace.UpdateIntervalHours)
	return nil
}

// collectConfiguredModels extracts unique Ollama and Google AI model names from config.
func collectConfiguredModels(cfg config.Config) (ollama, google []string) {
	ollamaSeen := map[string]bool{}
	googleSeen := map[string]bool{}

	for _, mc := range cfg.Models.All() {
		if mc.Model != "" && strings.Contains(mc.Provider, "ollama") {
			if !ollamaSeen[mc.Model] {
				ollamaSeen[mc.Model] = true
				ollama = append(ollama, mc.Model)
			}
		}
		if mc.GoogleModel != "" && strings.Contains(mc.Provider, "google") {
			if !googleSeen[mc.GoogleModel] {
				googleSeen[mc.GoogleModel] = true
				google = append(google, mc.GoogleModel)
			}
		}
	}
	return
}
