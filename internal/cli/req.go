package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func newReqCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "req <requirement>",
		Short: "Submit a new requirement for planning",
		Long:  "Takes a requirement as a positional argument, decomposes it into stories via the Tech Lead LLM, and prints the plan.",
		Args:  cobra.ExactArgs(1),
		RunE:  runReq,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runReq(cmd *cobra.Command, args []string) error {
	requirement := args[0]

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// Determine LLM client from config
	client, err := buildLLMClient(s.Config.Models.TechLead.Provider)
	if err != nil {
		return err
	}

	// Generate requirement ID
	reqID := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()

	// Determine repo path (current directory)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	planner := engine.NewPlanner(client, s.Config, s.Events, s.Proj)

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Planning requirement: %s\n", requirement)
	fmt.Fprintf(out, "Requirement ID: %s\n\n", reqID)

	result, err := planner.Plan(ctx, reqID, requirement, repoPath)
	if err != nil {
		return fmt.Errorf("planning failed: %w", err)
	}

	// Print plan summary
	fmt.Fprintf(out, "Plan created with %d stories:\n\n", len(result.Stories))

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
	fmt.Fprintf(out, "Run 'nxd status --req %s' to track progress.\n", reqID)

	return nil
}

// buildLLMClient creates an LLM client based on the provider name.
func buildLLMClient(provider string) (llm.Client, error) {
	switch provider {
	case "anthropic":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
		}
		return llm.NewAnthropicClient(apiKey), nil
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
		}
		return llm.NewOpenAIClient(apiKey), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", provider)
	}
}
