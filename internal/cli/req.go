package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/plugin"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// activePluginProviders holds plugin-contributed LLM providers for use by
// buildLLMClient. Set after loading plugins in runReq or runResume.
var activePluginProviders map[string]*plugin.SubprocessInfo

// buildLLMClientFunc is the function used to create LLM clients. It defaults
// to buildLLMClientDefault but can be overridden in tests to inject mock clients.
var buildLLMClientFunc = buildLLMClientDefault

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
	cmd.Flags().Bool("godmode", false, "skip permission prompts on LLM calls (fully autonomous)")
	cmd.Flags().Bool("review", false, "Pause after planning for manual review")
	cmd.Flags().Bool("dry-run", false, "Simulate LLM responses for pipeline testing (no API calls)")
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

	out := cmd.OutOrStdout()

	// Load plugins.
	pluginDir := expandHome("~/.nxd/plugins")
	pm, pluginErr := plugin.LoadPlugins(s.Config.Plugins, pluginDir)
	if pluginErr != nil {
		fmt.Fprintf(out, "Warning: plugin loading failed: %v\n", pluginErr)
		pm = plugin.EmptyManager()
	}

	// Apply plugin prompts and playbooks.
	var pbEntries []agent.PluginPlaybookEntry
	for _, pb := range pm.Playbooks {
		pbEntries = append(pbEntries, agent.PluginPlaybookEntry{
			Content:    pb.Content,
			InjectWhen: pb.InjectWhen,
			Roles:      pb.Roles,
		})
	}
	agent.SetPluginState(pbEntries, pm.Prompts)

	// Make plugin providers available to buildLLMClient.
	activePluginProviders = pm.Providers

	// Acquire pipeline lock to prevent concurrent runs.
	stateDir := expandHome(s.Config.Workspace.StateDir)
	lock, err := engine.AcquireLock(stateDir)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Determine LLM client — --godmode flag takes precedence over config
	godmode, _ := cmd.Flags().GetBool("godmode")
	if !godmode {
		godmode = s.Config.Planning.Godmode
	}
	client, err := buildLLMClient(s.Config.Models.TechLead.Provider, godmode)
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		client = llm.NewDryRunClient(200 * time.Millisecond)
		fmt.Fprintf(out, "[DRY RUN] Using simulated LLM responses\n")
	}

	// Generate requirement ID
	reqID := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()

	// Wrap LLM client with metrics tracking
	recorder := metrics.NewRecorder(filepath.Join(stateDir, "metrics.jsonl"))
	client = metrics.NewMetricsClient(client, recorder, reqID, "pipeline", "")

	// Determine repo path (current directory)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	planner := engine.NewPlanner(client, s.Config, s.Events, s.Proj)
	planner.SetProjectDir(expandHome(s.Config.Workspace.StateDir))

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	fmt.Fprintf(out, "Planning requirement: %s\n", requirement)
	fmt.Fprintf(out, "Requirement ID: %s\n\n", reqID)

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
		investigatorModel := s.Config.Models.Investigator
		inv := engine.NewInvestigator(client, investigatorModel.Model, investigatorModel.MaxTokens)
		inv.SetCommandAllowlist(s.Config.Investigation.CommandAllowlist)
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

	// Emit classification event
	classPayload := map[string]any{
		"req_id":      reqID,
		"req_type":    classification.Type,
		"is_existing": repoProfile.IsExisting,
		"confidence":  classification.Confidence,
	}
	classEvt := state.NewEvent(state.EventReqClassified, "", "", classPayload)
	s.Events.Append(classEvt)
	s.Proj.Project(classEvt)

	// Emit investigation event if report exists
	if report != nil {
		reportJSON, _ := json.Marshal(report)
		invEvt := state.NewEvent(state.EventInvestigationCompleted, "", "", map[string]any{
			"req_id": reqID,
			"report": string(reportJSON),
		})
		s.Events.Append(invEvt)
		s.Proj.Project(invEvt)
	}

	result, err := planner.PlanWithContext(ctx, reqID, requirement, repoPath, reqCtx)
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

	// If --review flag is set, pause for manual review before execution
	reviewMode, _ := cmd.Flags().GetBool("review")
	if reviewMode {
		evt := state.NewEvent(state.EventReqPendingReview, "", "", map[string]any{"id": reqID})
		s.Events.Append(evt)
		s.Proj.Project(evt)
		fmt.Fprintf(out, "\nPlan ready for review.\n")
		fmt.Fprintf(out, "  Review:  nxd status --req %s\n", reqID)
		fmt.Fprintf(out, "  Approve: nxd approve %s\n", reqID)
		fmt.Fprintf(out, "  Reject:  nxd reject %s\n", reqID)
		return nil
	}

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
// An optional godmode parameter controls whether permission prompts are skipped
// on runtimes that support it (e.g., Claude Code, Codex).
// It delegates to buildLLMClientFunc, which can be overridden in tests.
func buildLLMClient(provider string, godmode ...bool) (llm.Client, error) {
	return buildLLMClientFunc(provider, godmode...)
}

// buildLLMClientDefault is the production implementation of buildLLMClient.
func buildLLMClientDefault(provider string, godmode ...bool) (llm.Client, error) {
	_ = len(godmode) > 0 && godmode[0] // reserved for forward compatibility

	switch provider {
	case "ollama":
		var opts []llm.OllamaOption
		if host := os.Getenv("OLLAMA_HOST"); host != "" {
			opts = append(opts, llm.WithOllamaBaseURL(host))
		}
		return llm.NewOllamaClient("", opts...), nil
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
	case "google":
		apiKey := os.Getenv("GOOGLE_AI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("GOOGLE_AI_API_KEY not set")
		}
		return llm.NewGoogleClient(apiKey), nil
	case "google+ollama":
		var ollamaOpts []llm.OllamaOption
		if host := os.Getenv("OLLAMA_HOST"); host != "" {
			ollamaOpts = append(ollamaOpts, llm.WithOllamaBaseURL(host))
		}
		ollamaClient := llm.NewOllamaClient("", ollamaOpts...)

		apiKey := os.Getenv("GOOGLE_AI_API_KEY")
		if apiKey == "" {
			// No API key — degrade to Ollama only (not an error)
			log.Printf("[config] GOOGLE_AI_API_KEY not set, using Ollama only")
			return ollamaClient, nil
		}
		googleClient := llm.NewGoogleClient(apiKey)
		return llm.NewFallbackClient(googleClient, ollamaClient, 60*time.Second), nil
	default:
		if activePluginProviders != nil {
			if provInfo, ok := activePluginProviders[provider]; ok {
				return llm.NewSubprocessClient(provInfo.Command, 5*time.Minute), nil
			}
		}
		return nil, fmt.Errorf("unsupported LLM provider: %s (supported: ollama, anthropic, openai, google, google+ollama)", provider)
	}
}
