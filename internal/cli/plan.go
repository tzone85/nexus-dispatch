package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan [requirement]",
		Short: "Dry-run planning without persisting state",
		Long: `Runs the full planning pipeline (classify, investigate, plan) using temporary
stores that are deleted after. Nothing is persisted. Prints the plan and exits.

The requirement text can be provided as:
  - A positional argument:  nxd plan "Add a health check endpoint"
  - A file (--file/-f):     nxd plan --file requirements.md
  - Stdin:                  cat spec.md | nxd plan --file -`,
		Args: cobra.MaximumNArgs(1),
		RunE: runPlan,
	}
	cmd.Flags().StringP("file", "f", "", "read requirement from a file (use - for stdin)")
	cmd.SilenceUsage = true
	return cmd
}

func runPlan(cmd *cobra.Command, args []string) error {
	requirement, err := resolveRequirement(cmd, args)
	if err != nil {
		return err
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}

	// Create temporary directory for ephemeral stores
	tmpDir, err := os.MkdirTemp("", "nxd-plan-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	es, err := state.NewFileStore(filepath.Join(tmpDir, "events.jsonl"))
	if err != nil {
		return fmt.Errorf("open temp event store: %w", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(filepath.Join(tmpDir, "nxd.db"))
	if err != nil {
		return fmt.Errorf("open temp projection store: %w", err)
	}
	defer ps.Close()

	// Build LLM client
	client, err := buildLLMClient(cfg.Models.TechLead.Provider, cfg.Planning.Godmode)
	if err != nil {
		return err
	}

	// Use a synthetic req ID for the dry-run
	reqID := "plan-dry-run"

	// Determine repo path (current directory)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	planner := engine.NewPlanner(client, cfg, es, ps)

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dry-run planning: %s\n\n", requirement)

	// Stage 1: Classify repo
	repoProfile := engine.ClassifyRepo(repoPath)

	// Stage 2: Classify requirement (only for existing repos)
	var classification engine.RequirementClassification
	if repoProfile.IsExisting {
		classification, _ = engine.ClassifyRequirement(ctx, client, requirement, repoProfile)
		fmt.Fprintf(out, "Detected: %s codebase, requirement type: %s (confidence: %.0f%%)\n",
			repoProfile.Language, classification.Type, classification.Confidence*100)
	} else {
		classification = engine.RequirementClassification{Type: "feature", Confidence: 1.0}
		fmt.Fprintf(out, "Detected: greenfield project (%s)\n", repoProfile.Language)
	}

	// Stage 3: Investigate (only for existing repos)
	var report *engine.InvestigationReport
	if repoProfile.IsExisting {
		fmt.Fprintf(out, "Running codebase investigation...\n")
		investigatorModel := cfg.Models.Investigator
		inv := engine.NewInvestigator(client, investigatorModel.Model, investigatorModel.MaxTokens)
		inv.SetCommandAllowlist(cfg.Investigation.CommandAllowlist)
		report, err = inv.Investigate(ctx, repoPath)
		if err != nil {
			fmt.Fprintf(out, "Warning: investigation failed: %v (continuing without report)\n", err)
			report = nil
		} else {
			fmt.Fprintf(out, "Investigation complete: %d modules, %d smells, %d risk areas\n",
				len(report.Modules), len(report.CodeSmells), len(report.RiskAreas))
		}
	}

	// Build requirement context
	reqCtx := engine.NewRequirementContext(repoProfile, classification)
	if report != nil {
		reqCtx.Report = report
	}

	result, err := planner.PlanWithContext(ctx, reqID, requirement, repoPath, reqCtx)
	if err != nil {
		return fmt.Errorf("planning failed: %w", err)
	}

	// Print plan summary
	fmt.Fprintf(out, "\nDry-run plan with %d stories:\n\n", len(result.Stories))

	totalComplexity := 0
	for i, story := range result.Stories {
		deps := "none"
		if len(story.DependsOn) > 0 {
			deps = fmt.Sprintf("%v", story.DependsOn)
		}
		fmt.Fprintf(out, "  %d. [%s] %s (complexity: %d, deps: %s)\n",
			i+1, story.ID, story.Title, story.Complexity, deps)
		totalComplexity += story.Complexity
	}

	fmt.Fprintf(out, "\nTotal complexity: %d story points\n", totalComplexity)
	fmt.Fprintf(out, "\nThis was a dry run. No state was persisted.\n")
	fmt.Fprintf(out, "Run 'nxd req' to submit for real.\n")

	return nil
}
