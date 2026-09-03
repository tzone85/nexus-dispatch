package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
)

const (
	maxInvestigationIterations = 20
	maxReadFileChars           = 8000
	maxCommandOutputChars      = 4000
)

// InvestigationReport is the structured output of a codebase investigation,
// covering architecture, health, code quality, and risk areas.
type InvestigationReport struct {
	Summary         string       `json:"summary"`
	EntryPoints     []string     `json:"entry_points"`
	Modules         []ModuleInfo `json:"modules"`
	BuildStatus     HealthStatus `json:"build_status"`
	TestStatus      HealthStatus `json:"test_status"`
	CodeSmells      []CodeSmell  `json:"code_smells"`
	RiskAreas       []RiskArea   `json:"risk_areas"`
	Conventions     []Convention `json:"conventions"`
	Recommendations []string     `json:"recommendations"`
}

// ModuleInfo describes a discovered module within the codebase.
type ModuleInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
	LineCount int    `json:"line_count"`
	HasTests  bool   `json:"has_tests"`
}

// HealthStatus represents the pass/fail status of a build or test step.
type HealthStatus struct {
	Passes   bool    `json:"passes"`
	Output   string  `json:"output,omitempty"`
	Count    int     `json:"count,omitempty"`
	Coverage float64 `json:"coverage,omitempty"`
}

// CodeSmell describes a code quality issue found during investigation.
type CodeSmell struct {
	File        string `json:"file"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// RiskArea identifies a file or area with elevated risk.
type RiskArea struct {
	File     string `json:"file"`
	Reason   string `json:"reason"`
	Severity string `json:"severity"`
}

// Convention describes a coding pattern or convention detected in the codebase.
type Convention struct {
	Area        string `json:"area"`
	Pattern     string `json:"pattern"`
	ExampleFile string `json:"example_file"`
}

// Investigator runs a tool-calling loop against an LLM to investigate a
// codebase and produce a structured InvestigationReport.
type Investigator struct {
	client           llm.Client
	model            string
	maxTokens        int
	commandAllowlist []string
	sandbox          runtime.CommandSandbox
}

// NewInvestigator creates an Investigator backed by the given LLM client.
func NewInvestigator(client llm.Client, model string, maxTokens int) *Investigator {
	return &Investigator{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}
}

// SetCommandAllowlist configures the allowlist for run_command tool calls.
// Matching is argv-aware (see internal/runtime/allowlist.go). An EMPTY
// allowlist denies every command — the old "empty = allow all" behaviour let
// a model whose context contains untrusted repository text run anything.
func (inv *Investigator) SetCommandAllowlist(allowlist []string) {
	inv.commandAllowlist = allowlist
}

// SetSandbox overrides the sandbox used for run_command. nil (default) uses
// runtime.DefaultSandbox(), which resume.go/req.go configure from nxd.yaml.
func (inv *Investigator) SetSandbox(sb runtime.CommandSandbox) {
	inv.sandbox = sb
}

func (inv *Investigator) sandboxOrDefault() runtime.CommandSandbox {
	if inv.sandbox != nil {
		return inv.sandbox
	}
	return runtime.DefaultSandbox()
}

// investigatorCommandTimeout bounds a single run_command.
const investigatorCommandTimeout = 2 * time.Minute

// isCommandAllowed reports whether command passes the allowlist when run in
// repoPath. It delegates to runtime.CheckCommand, which rejects shell
// metacharacters, exec-style flags (find -exec, go test -exec, ...), env-var
// prefixes, and any absolute / ~ / .. path that leaves the repository — so
// `cat /etc/passwd`, `cat ~/.aws/credentials` and `find / -name '*.pem'` are
// all denied even though cat and find are allowlisted.
func (inv *Investigator) isCommandAllowed(command string) bool {
	return inv.checkCommand(command, "") == nil
}

func (inv *Investigator) checkCommand(command, repoPath string) error {
	return runtime.CheckCommand(command, inv.commandAllowlist, repoPath)
}

// Investigate runs the 7-phase investigation loop on the repository at
// repoPath. It returns a structured report or an error if the model fails to
// submit a report within the iteration limit.
func (inv *Investigator) Investigate(ctx context.Context, repoPath string) (*InvestigationReport, error) {
	tools := agent.InvestigatorTools()
	systemPrompt := agent.InvestigatorSystemPrompt()

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("Investigate the codebase at: %s\nFollow all 7 phases, then call submit_report.", repoPath),
		},
	}

	for i := 0; i < maxInvestigationIterations; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("investigation cancelled: %w", err)
		}

		resp, err := inv.client.Complete(ctx, llm.CompletionRequest{
			Model:     inv.model,
			MaxTokens: inv.maxTokens,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return nil, fmt.Errorf("investigation LLM call (iteration %d): %w", i+1, err)
		}

		// No tool calls — append the assistant text plus a corrective user
		// nudge, then continue. Without the nudge, small local models keep
		// chatting in prose until the iteration cap kills the investigation.
		if len(resp.ToolCalls) == 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleAssistant,
				Content: resp.Content,
			},
				llm.Message{
					Role:    llm.RoleUser,
					Content: "Respond only with tool calls (read_file, run_command), and call submit_report as soon as you have enough information. Do not reply in prose.",
				})
			continue
		}

		// Append assistant message with tool calls
		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Process each tool call
		for _, tc := range resp.ToolCalls {
			switch tc.Name {
			case "submit_report":
				return parseSubmitReport(tc.Arguments)

			case "read_file":
				result := inv.handleReadFile(repoPath, tc.Arguments)
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				})

			case "run_command":
				result := inv.handleRunCommand(ctx, repoPath, tc.Arguments)
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				})

			default:
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Name),
					ToolCallID: tc.ID,
				})
			}
		}
	}

	return nil, fmt.Errorf("investigation did not complete within %d iterations", maxInvestigationIterations)
}

// handleReadFile reads a file relative to repoPath with path traversal
// protection (lexical AND after symlink resolution) and content truncation.
func (inv *Investigator) handleReadFile(repoPath string, args json.RawMessage) string {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return fmt.Sprintf("error: invalid read_file arguments: %v", err)
	}

	absTarget, err := resolveRepoPath(repoPath, params.Path)
	if err != nil {
		return "error: path traversal detected — access denied"
	}

	data, err := os.ReadFile(absTarget)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	content := string(data)
	if len(content) > maxReadFileChars {
		content = content[:maxReadFileChars] + "\n... [truncated, file exceeds 8000 chars]"
	}
	return content
}

// resolveRepoPath joins rel onto repoPath and verifies the result stays inside
// the repository both lexically and after filepath.EvalSymlinks — a committed
// symlink pointing at /etc or $HOME must not be readable through the tool.
// Mirrors runtime/gemma.go safePath.
func resolveRepoPath(repoPath, rel string) (string, error) {
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") {
		return "", fmt.Errorf("absolute or home-relative path")
	}
	target := filepath.Clean(filepath.Join(absRepo, rel))
	if !insideDir(target, absRepo) {
		return "", fmt.Errorf("outside repository")
	}
	realRepo, err := filepath.EvalSymlinks(absRepo)
	if err != nil {
		realRepo = absRepo
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		// Missing file: the lexical check above already passed; let ReadFile
		// report the real error.
		return target, nil
	}
	if !insideDir(realTarget, realRepo) {
		return "", fmt.Errorf("symlink escapes repository")
	}
	return realTarget, nil
}

func insideDir(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// handleRunCommand executes an allowlisted command in the repo directory via
// the configured sandbox, with output truncation.
func (inv *Investigator) handleRunCommand(ctx context.Context, repoPath string, args json.RawMessage) string {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return fmt.Sprintf("error: invalid run_command arguments: %v", err)
	}

	if err := inv.checkCommand(params.Command, repoPath); err != nil {
		return fmt.Sprintf("error: command not in allowlist: %s (%v)", params.Command, err)
	}
	argv, err := runtime.TokenizeCommand(params.Command)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	res, err := inv.sandboxOrDefault().Exec(ctx, repoPath, argv, investigatorCommandTimeout)
	result := res.Combined()
	switch {
	case err != nil:
		result = result + "\nexit error: " + err.Error()
	case res.ExitCode != 0:
		result = result + fmt.Sprintf("\nexit error: exit status %d", res.ExitCode)
	}

	if len(result) > maxCommandOutputChars {
		result = result[:maxCommandOutputChars] + "\n... [truncated, output exceeds 4000 chars]"
	}
	return result
}

// parseSubmitReport converts the submit_report tool call arguments into a
// structured InvestigationReport.
func parseSubmitReport(args json.RawMessage) (*InvestigationReport, error) {
	// Parse the raw report fields — the tool schema uses flat keys that differ
	// slightly from the InvestigationReport struct fields.
	var raw struct {
		Summary         string       `json:"summary"`
		EntryPoints     []string     `json:"entry_points"`
		Modules         []ModuleInfo `json:"modules"`
		BuildPasses     bool         `json:"build_passes"`
		TestPasses      bool         `json:"test_passes"`
		TestCount       int          `json:"test_count"`
		CoveragePct     float64      `json:"coverage_pct"`
		CodeSmells      []CodeSmell  `json:"code_smells"`
		RiskAreas       []RiskArea   `json:"risk_areas"`
		Conventions     []Convention `json:"conventions"`
		Recommendations []string     `json:"recommendations"`
	}

	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("parse submit_report: %w", err)
	}

	report := &InvestigationReport{
		Summary:     raw.Summary,
		EntryPoints: raw.EntryPoints,
		Modules:     raw.Modules,
		BuildStatus: HealthStatus{
			Passes: raw.BuildPasses,
		},
		TestStatus: HealthStatus{
			Passes:   raw.TestPasses,
			Count:    raw.TestCount,
			Coverage: raw.CoveragePct,
		},
		CodeSmells:      raw.CodeSmells,
		RiskAreas:       raw.RiskAreas,
		Conventions:     raw.Conventions,
		Recommendations: raw.Recommendations,
	}

	// Ensure slices are non-nil for consistent JSON output
	if report.EntryPoints == nil {
		report.EntryPoints = []string{}
	}
	if report.Modules == nil {
		report.Modules = []ModuleInfo{}
	}
	if report.CodeSmells == nil {
		report.CodeSmells = []CodeSmell{}
	}
	if report.RiskAreas == nil {
		report.RiskAreas = []RiskArea{}
	}
	if report.Conventions == nil {
		report.Conventions = []Convention{}
	}
	if report.Recommendations == nil {
		report.Recommendations = []string{}
	}

	return report, nil
}
