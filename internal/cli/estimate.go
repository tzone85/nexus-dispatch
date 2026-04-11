package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func newEstimateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "estimate [requirement]",
		Short: "Estimate cost and effort for a requirement",
		Long:  "Produces a client-facing quote and internal cost projection.\nUses the planner for accurate decomposition, or --quick for heuristic.",
		Args:  cobra.ExactArgs(1),
		RunE:  runEstimate,
	}
	cmd.Flags().Bool("quick", false, "Use heuristic estimation (no LLM call)")
	cmd.Flags().Float64("rate", 0, "Override hourly rate (USD)")
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().Bool("save", false, "Persist estimate as event")
	return cmd
}

func runEstimate(cmd *cobra.Command, args []string) error {
	requirement := args[0]
	quick, _ := cmd.Flags().GetBool("quick")
	rateOverride, _ := cmd.Flags().GetFloat64("rate")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	save, _ := cmd.Flags().GetBool("save")

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return fmt.Errorf("loading stores: %w", err)
	}
	defer s.Close()

	var lc llm.Client
	repoPath := ""
	if !quick {
		lc, err = buildLLMClient(s.Config.Models.TechLead.Provider)
		if err != nil {
			return fmt.Errorf("creating LLM client: %w", err)
		}
		repoPath, _ = os.Getwd()
	}

	estimator := engine.NewEstimator(lc, s.Config, s.Events, s.Proj)

	est, err := estimator.Estimate(cmd.Context(), requirement, repoPath, engine.EstimateOptions{
		Quick:        quick,
		RateOverride: rateOverride,
		Save:         save,
	})
	if err != nil {
		return fmt.Errorf("estimation failed: %w", err)
	}

	if jsonOutput {
		return printEstimateJSON(est)
	}
	return printEstimateTable(est)
}

func printEstimateJSON(est engine.Estimate) error {
	data, err := json.MarshalIndent(est, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func printEstimateTable(est engine.Estimate) error {
	if est.IsQuick {
		return printQuickTable(est)
	}
	return printLiveTable(est)
}

func printLiveTable(est engine.Estimate) error {
	header := fmt.Sprintf("Estimate: %s", est.Requirement)
	fmt.Println(header)
	fmt.Println(strings.Repeat("\u2500", len(header)))
	fmt.Println()

	fmt.Printf("Stories  Complexity   Est. Hours    Client Quote ($%.0f/hr)\n", est.Summary.Rate)
	fmt.Println("\u2500\u2500\u2500\u2500\u2500\u2500\u2500  \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500   \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Printf("  %d       %3d pts     %.0f \u2013 %.0fh      $%.0f \u2013 $%.0f\n",
		est.Summary.StoryCount,
		est.Summary.TotalPoints,
		est.Summary.HoursLow,
		est.Summary.HoursHigh,
		est.Summary.QuoteLow,
		est.Summary.QuoteHigh,
	)
	fmt.Println()

	fmt.Println("Story Breakdown:")
	for i, s := range est.Stories {
		fmt.Printf("  #%d  %-35s %2d pts   %.0f\u2013%.0fh   (%s)\n",
			i+1, s.Title, s.Complexity, s.HoursLow, s.HoursHigh, s.Role)
	}
	fmt.Println()

	if est.Summary.LLMCost == 0 {
		fmt.Println("LLM Cost:  $0.00  (Ollama Free)")
	} else {
		fmt.Printf("LLM Cost:  $%.2f\n", est.Summary.LLMCost)
	}
	fmt.Printf("Margin:    ~%.0f%%\n", est.Summary.MarginPercent)

	return nil
}

func printQuickTable(est engine.Estimate) error {
	header := fmt.Sprintf("Quick Estimate: %s", est.Requirement)
	fmt.Println(header)
	fmt.Println(strings.Repeat("\u2500", len(header)))
	fmt.Println()

	fmt.Printf("Est. Stories: %d  |  Est. Hours: %.0f\u2013%.0fh  |  Quote: $%.0f \u2013 $%.0f\n",
		est.Summary.StoryCount,
		est.Summary.HoursLow,
		est.Summary.HoursHigh,
		est.Summary.QuoteLow,
		est.Summary.QuoteHigh,
	)
	fmt.Println()
	fmt.Println("\u26a0 Heuristic only \u2014 run without --quick for planner-based estimate")

	return nil
}
