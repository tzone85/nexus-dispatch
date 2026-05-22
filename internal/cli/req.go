package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	cmd.Flags().Bool("background", false, "self-daemonize after planning: fork a detached child process and exit; tail logs with 'nxd req-logs <req-id>'")
	cmd.SilenceUsage = true
	return cmd
}

// forkReqDaemon forks a detached child process that runs `nxd resume <reqID>`.
// The child is placed in its own process group (Setsid) so that macOS
// app-nap and parent-shell teardown cannot kill it.
//
// stdout+stderr of the child are redirected to the caller-supplied log file.
// This is a pure construction function — it does NOT exec. Tests can call it
// without side effects by inspecting the returned Cmd.
func forkReqDaemon(self, reqID string, extraArgs []string) *exec.Cmd {
	argv := append([]string{"resume", reqID}, extraArgs...)
	cmd := exec.Command(self, argv...)

	// Detach from the current process group so parent-shell teardown cannot
	// kill the child (macOS app-nap prevention).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "." // inherit cwd
	return cmd
}

// reqLogPath returns the path of the daemon log file for the given state
// directory and requirement ID.
func reqLogPath(stateDir, reqID string) string {
	return filepath.Join(stateDir, "logs", "req-"+reqID+".log")
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

	// ZeeSpec adoption: if a .spec/ folder exists in the target repo,
	// prepend its assembled content to the requirement so the planner sees
	// structured 5W1H context. Reduces hallucination on greenfield setup.
	if specCtx := LoadSpecForRequirement(repoPath); specCtx != "" {
		fmt.Fprintf(out, "[spec] augmenting requirement with .spec/ context (%d bytes)\n", len(specCtx))
		requirement = specCtx + "\n---\n\n# User requirement\n\n" + requirement
	}

	planner := engine.NewPlanner(client, s.Config, s.Events, s.Proj)
	planner.SetProjectDir(expandHome(s.Config.Workspace.StateDir))

	// Allow generous timeout for planning: investigation (up to 20 LLM calls)
	// + planning (1-2 calls) can take 10+ minutes on local GPU models.
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
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

	planStart := time.Now()
	result, err := planner.PlanWithContext(ctx, reqID, requirement, repoPath, reqCtx)
	if err != nil {
		engine.EmitStageCompleted(s.Events, s.Proj, "planner", "", "plan", "failure", planStart)
		return fmt.Errorf("planning failed: %w", err)
	}
	engine.EmitStageCompleted(s.Events, s.Proj, "planner", "", "plan", "success", planStart)

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

	// --background: self-daemonize by forking a detached child that runs
	// `nxd resume <reqID>`. The parent prints the PID and log path, then exits 0.
	// This prevents macOS app-nap and parent-shell teardown from killing the run.
	background, _ := cmd.Flags().GetBool("background")
	if background {
		stateDir := expandHome(s.Config.Workspace.StateDir)
		logDir := filepath.Join(stateDir, "logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		lp := reqLogPath(stateDir, reqID)

		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve self path: %w", err)
		}

		// Carry forward --godmode, --dry-run, and --config to the child.
		var childExtra []string
		if godmode {
			childExtra = append(childExtra, "--godmode")
		}
		if dryRun {
			childExtra = append(childExtra, "--dry-run")
		}
		cfgPath, _ := cmd.Flags().GetString("config")
		if cfgPath != "" && cfgPath != "nxd.yaml" {
			childExtra = append(childExtra, "--config", cfgPath)
		}

		child := forkReqDaemon(self, reqID, childExtra)

		// Open log file and attach to child's stdout+stderr.
		lf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		child.Stdout = lf
		child.Stderr = lf

		devNull, err := os.Open(os.DevNull)
		if err != nil {
			lf.Close()
			return fmt.Errorf("open /dev/null: %w", err)
		}
		child.Stdin = devNull

		if err := child.Start(); err != nil {
			lf.Close()
			devNull.Close()
			return fmt.Errorf("fork daemon: %w", err)
		}
		// Close our copies of the file handles; child has its own fd via Start().
		lf.Close()
		devNull.Close()

		fmt.Fprintf(out, "Requirement %s dispatched (daemon pid %d).\n", reqID, child.Process.Pid)
		fmt.Fprintf(out, "Tail logs: nxd req-logs %s\n", reqID)
		fmt.Fprintf(out, "Log file:  %s\n", lp)
		return nil
	}

	fmt.Fprintf(out, "Run 'nxd status --req %s' to track progress.\n", reqID)
	fmt.Fprintf(out, "Run 'nxd resume %s' to dispatch agents.\n", reqID)

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
//
// H7: every client returned here is wrapped with a SanitizingClient so that
// LLM-generated content (review feedback, manager diagnoses, etc.) is screened
// for prompt-injection markers and embedded credentials before downstream
// consumers see it.
func buildLLMClient(provider string, godmode ...bool) (llm.Client, error) {
	c, err := buildLLMClientFunc(provider, godmode...)
	if err != nil {
		return nil, err
	}
	return llm.NewSanitizingClient(c, provider), nil
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
