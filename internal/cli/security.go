package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/security"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Security agent: scan repositories and inspect the knowledge base",
		Long: `The security agent combines deterministic scanners (gosec, govulncheck,
gitleaks, semgrep, npm audit) with an optional LLM threat-model review driven by
a growable knowledge base (OWASP Top 10 + CWE baseline that learns new
vulnerability classes from confirmed findings).`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newSecurityScanCmd())
	cmd.AddCommand(newSecurityKBCmd())
	return cmd
}

func newSecurityScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan [repo-path]",
		Short: "Run a security scan on a repository",
		Long: `Scans a repository with every applicable, installed scanner and (optionally)
an LLM threat-model review. Findings are reported by severity; applicable
scanners that are not installed are listed so coverage gaps are never silent.

Exit code is non-zero when a finding meets or exceeds --min (default: high),
so the command is CI-friendly.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runSecurityScan,
	}
	cmd.Flags().Bool("json", false, "Output the report as JSON")
	cmd.Flags().Bool("llm", false, "Add an LLM threat-model review (requires a configured model; reads source files)")
	cmd.Flags().String("min", "high", "Severity that makes the command exit non-zero: critical|high|medium|low")
	cmd.SilenceUsage = true
	return cmd
}

func runSecurityScan(cmd *cobra.Command, args []string) error {
	jsonOut, _ := cmd.Flags().GetBool("json")
	useLLM, _ := cmd.Flags().GetBool("llm")
	minStr, _ := cmd.Flags().GetString("min")

	repoPath, err := resolveScanPath(args)
	if err != nil {
		return err
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg := loadConfigOrDefault(cfgPath)

	kbPath := securityKBPath(cfg)

	// Event/projection stores so scans are auditable. Use an in-memory
	// projection (scan results are informational; the event log is the record).
	es, err := state.NewFileStore(filepath.Join(expandHome(cfg.Workspace.StateDir), "events.jsonl"))
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer func() { _ = es.Close() }()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		return fmt.Errorf("open projection store: %w", err)
	}
	defer func() { _ = ps.Close() }()

	// LLM review is opt-in: it needs file access, so it runs godmode (skip
	// permission prompts) to stay non-interactive. Default is scanners-only
	// (nil client ⇒ the gate runs deterministic scanners only).
	var llmClient llm.Client
	model := cfg.Models.Senior.Model
	maxTokens := cfg.Models.Senior.MaxTokens
	if useLLM {
		built, buildErr := buildLLMClient(cfg.Models.Senior.Provider, true)
		if buildErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "warning: LLM review unavailable (%v) — running scanners only\n", buildErr)
		} else {
			llmClient = built
		}
	}

	gate := engine.NewSecurityGate(
		llmClient, model, maxTokens, kbPath,
		security.ParseSeverity(cfg.Security.GateSeverity),
		cfg.Security.AutoLearn, es, ps,
	)

	report, err := gate.ScanRepo(context.Background(), repoPath)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), report.FormatMarkdown())
	}

	// CI-friendly exit code.
	min := security.ParseSeverity(minStr)
	if report.HasAtLeast(min) {
		return fmt.Errorf("security scan found %s+ findings", min)
	}
	return nil
}

func newSecurityKBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kb",
		Short: "Show the security knowledge base (version, rules, learned classes)",
		Args:  cobra.NoArgs,
		RunE:  runSecurityKB,
	}
	cmd.Flags().Bool("json", false, "Output the knowledge base as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func runSecurityKB(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg := loadConfigOrDefault(cfgPath)
	kb, err := security.LoadKnowledgeBase(securityKBPath(cfg))
	if err != nil {
		return fmt.Errorf("load knowledge base: %w", err)
	}
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(kb)
	}
	out := cmd.OutOrStdout()
	baseline, learned := 0, 0
	for _, r := range kb.Rules {
		if r.Source == security.RuleLearned {
			learned++
		} else {
			baseline++
		}
	}
	fmt.Fprintf(out, "Security knowledge base v%d — %d rules (%d baseline, %d learned)\n\n",
		kb.Version, len(kb.Rules), baseline, learned)
	for _, r := range kb.Rules {
		marker := " "
		if r.Source == security.RuleLearned {
			marker = "+"
		}
		fmt.Fprintf(out, " %s [%s] %s — %s\n", marker, r.ID, r.Title, r.Severity)
	}
	return nil
}

// resolveScanPath resolves the target repo to an absolute path (cwd default).
func resolveScanPath(args []string) (string, error) {
	p := ""
	if len(args) > 0 {
		p = args[0]
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		p = cwd
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	return abs, nil
}

// loadConfigOrDefault loads the project config, falling back to DefaultConfig
// when none exists. The security scan is a standalone tool meant to run in ANY
// repo (which usually has no nxd.yaml), and it only needs model/KB/severity
// defaults — so a missing config is not an error here.
func loadConfigOrDefault(cfgPath string) config.Config {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return config.DefaultConfig()
	}
	return cfg
}

// securityKBPath resolves where the knowledge base persists: the configured path
// or <state_dir>/security/knowledge.json.
func securityKBPath(cfg config.Config) string {
	if cfg.Security.KBPath != "" {
		return expandHome(cfg.Security.KBPath)
	}
	return filepath.Join(expandHome(cfg.Workspace.StateDir), "security", "knowledge.json")
}
