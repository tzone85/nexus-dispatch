package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func newReqCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "req [requirement]",
		Short: "Submit a new requirement for planning",
		Long: `Decomposes a requirement into stories via the Tech Lead LLM and prints the plan.

The requirement text can be provided as:
  - A positional argument:  nxd req "Add a health check endpoint"
  - A file (--file/-f):     nxd req --file requirements.md
  - Stdin:                  cat spec.md | nxd req --file -`,
		Args: cobra.MaximumNArgs(1),
		RunE: runReq,
	}
	cmd.Flags().StringP("file", "f", "", "read requirement from a file (use - for stdin)")
	cmd.SilenceUsage = true
	return cmd
}

func runReq(cmd *cobra.Command, args []string) error {
	requirement, err := resolveRequirement(cmd, args)
	if err != nil {
		return err
	}

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

// resolveRequirement reads the requirement text from either the --file flag,
// stdin (when --file is "-"), or the positional argument.
func resolveRequirement(cmd *cobra.Command, args []string) (string, error) {
	filePath, _ := cmd.Flags().GetString("file")

	switch {
	case filePath != "" && len(args) > 0:
		return "", fmt.Errorf("provide either a positional argument or --file, not both")
	case filePath == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("stdin was empty")
		}
		return text, nil
	case filePath != "":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read file %s: %w", filePath, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("file %s is empty", filePath)
		}
		return text, nil
	case len(args) > 0:
		return args[0], nil
	default:
		return "", fmt.Errorf("provide a requirement as an argument or via --file")
	}
}

// buildLLMClient creates an LLM client based on the provider name.
// For the "anthropic" provider, it prefers the Claude Code CLI (which uses
// the user's subscription at no per-token cost) and falls back to direct API
// calls only when the CLI is not installed.
func buildLLMClient(provider string) (llm.Client, error) {
	switch provider {
	case "ollama":
		var opts []llm.OllamaOption
		if host := os.Getenv("OLLAMA_HOST"); host != "" {
			opts = append(opts, llm.WithOllamaBaseURL(host))
		}
		return llm.NewOllamaClient("", opts...), nil
	case "cli", "claude-cli":
		return llm.NewClaudeCLIClient(), nil
	case "anthropic":
		// Prefer Claude CLI (uses subscription, no API credits).
		if _, err := exec.LookPath("claude"); err == nil {
			return llm.NewClaudeCLIClient(), nil
		}
		// Fall back to direct API if CLI not available.
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("claude CLI not found and ANTHROPIC_API_KEY not set")
		}
		return llm.NewAnthropicClient(apiKey), nil
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
		}
		return llm.NewOpenAIClient(apiKey), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s (supported: ollama, anthropic, openai, cli)", provider)
	}
}
