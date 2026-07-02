package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// verifyFunc runs a verification cycle against repoDir and returns the result.
// It is a seam so tests can script red/green sequences without a real toolchain.
type verifyFunc func(ctx context.Context, repoDir string, cycle int) VerificationResult

// completionFixTimeout bounds a single auto-fix agent invocation.
const completionFixTimeout = 15 * time.Minute

// CompletionGate guards the REQ_COMPLETED signal. When every story has merged,
// it verifies the composed mainline (build + tests + artifacts) and — when the
// build is red — runs a bounded auto-fix loop: dispatch a fix agent, re-verify,
// repeat up to maxCycles. The requirement is only safe to mark complete when
// verification passes; otherwise the caller emits REQ_BLOCKED.
//
// This closes the long-standing gap where per-story QA (run in isolated
// worktrees) could not see cross-story drift, so a requirement was reported
// complete on code that does not compile.
type CompletionGate struct {
	client     llm.Client // godmode agent that applies fixes; nil ⇒ hard gate only
	model      string
	maxTokens  int
	maxCycles  int
	baseBranch string
	eventStore state.EventStore
	projStore  state.ProjectionStore

	// Seams (default to real implementations; overridden in tests).
	verify verifyFunc
	pull   func(repoDir, baseBranch string)
}

// NewCompletionGate constructs a gate. maxCycles is the number of auto-fix
// attempts before giving up; 0 makes the gate a pure pass/block check with no
// auto-fix. A nil client also degrades the gate to hard-gate behaviour.
func NewCompletionGate(
	client llm.Client,
	model string,
	maxTokens, maxCycles int,
	baseBranch string,
	es state.EventStore,
	ps state.ProjectionStore,
) *CompletionGate {
	if baseBranch == "" {
		baseBranch = "main"
	}
	return &CompletionGate{
		client:     client,
		model:      model,
		maxTokens:  maxTokens,
		maxCycles:  maxCycles,
		baseBranch: baseBranch,
		eventStore: es,
		projStore:  ps,
		verify: func(ctx context.Context, repoDir string, cycle int) VerificationResult {
			return RunVerificationLoop(ctx, repoDir, cycle)
		},
		pull: func(repoDir, baseBranch string) {
			pullBaseAfterMerge(repoDir, baseBranch)
		},
	}
}

// Run verifies the composed mainline and auto-fixes a red build up to maxCycles
// times. It returns true when verification is green (safe to emit
// REQ_COMPLETED) and false when the mainline remains red after exhausting the
// auto-fix budget (caller should emit REQ_BLOCKED).
func (g *CompletionGate) Run(ctx context.Context, reqID, repoDir string) bool {
	cycle := 1
	res := g.verify(ctx, repoDir, cycle)
	if !ShouldRunFixCycle(res) {
		log.Printf("[gate] %s: verification clean on first pass — completion permitted", reqID)
		return true
	}

	for attempt := 1; attempt <= g.maxCycles; attempt++ {
		g.recordRedCycle(reqID, repoDir, res)

		if g.client == nil {
			log.Printf("[gate] %s: no auto-fix client configured — hard-gating on red build", reqID)
			break
		}

		log.Printf("[gate] %s: auto-fix cycle %d/%d — dispatching fix agent for %d gap(s)",
			reqID, attempt, g.maxCycles, len(res.Gaps))
		if err := g.applyFix(ctx, repoDir, res); err != nil {
			log.Printf("[gate] %s: auto-fix cycle %d failed to dispatch: %v", reqID, attempt, err)
			break
		}

		g.pull(repoDir, g.baseBranch)

		cycle++
		res = g.verify(ctx, repoDir, cycle)
		if !ShouldRunFixCycle(res) {
			log.Printf("[gate] %s: verification clean after auto-fix cycle %d — completion permitted",
				reqID, attempt)
			return true
		}
	}

	log.Printf("[gate] %s: mainline still red after %d auto-fix cycle(s) — BLOCKING completion",
		reqID, g.maxCycles)
	return false
}

// recordRedCycle persists the gap requirement to .nxd-fix-gaps.md for operator
// transparency. Best-effort: a write failure is logged, never fatal.
func (g *CompletionGate) recordRedCycle(reqID, repoDir string, res VerificationResult) {
	fixReq := GapsToRequirement(res.Gaps, filepath.Base(repoDir))
	if fixReq == "" {
		return
	}
	fixPath := filepath.Join(repoDir, ".nxd-fix-gaps.md")
	if err := os.WriteFile(fixPath, []byte(fixReq), 0o600); err != nil {
		log.Printf("[gate] %s: failed to write %s: %v", reqID, fixPath, err)
	}
}

// applyFix dispatches a single synchronous fix-agent run. The agent runs in
// godmode (skip-permissions) in the project's working directory, so it can
// read the codebase, edit files, run the build/tests, and commit + push the
// reconciliation to the base branch.
func (g *CompletionGate) applyFix(ctx context.Context, repoDir string, res VerificationResult) error {
	fixCtx, cancel := context.WithTimeout(ctx, completionFixTimeout)
	defer cancel()

	prompt := g.buildFixPrompt(repoDir, res)
	_, err := g.client.Complete(fixCtx, llm.CompletionRequest{
		Model:     g.model,
		MaxTokens: g.maxTokens,
		System: "You are a Tech Lead repairing a multi-story integration on the main branch. " +
			"The composed codebase does not build or its tests fail. Make the minimal changes " +
			"needed to turn the build and tests green, then commit and push to the base branch.",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	return err
}

// buildFixPrompt describes the failing build/tests and the exact remediation
// contract (fix → build → test → commit → push).
func (g *CompletionGate) buildFixPrompt(repoDir string, res VerificationResult) string {
	var sb strings.Builder
	sb.WriteString("The main branch of this repository is the composed result of several merged stories ")
	sb.WriteString("and is currently failing verification.\n\n")

	fmt.Fprintf(&sb, "Build passes: %v\n", res.BuildPasses)
	fmt.Fprintf(&sb, "Tests: %d passing / %d failing / %d total\n\n", res.TestsPassing, res.TestsFailing, res.TestsTotal)

	if len(res.Gaps) > 0 {
		sb.WriteString("Gaps detected:\n")
		for _, gap := range res.Gaps {
			fmt.Fprintf(&sb, "  - [%s/%s] %s: %s\n", gap.Category, gap.Severity, gap.File, gap.Detail)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Working directory: ")
	sb.WriteString(repoDir)
	sb.WriteString("\n\nDo the following, in order:\n")
	sb.WriteString("1. Investigate the failing build/tests (read the affected files and error output).\n")
	sb.WriteString("2. Apply the MINIMAL change that reconciles the cross-story break — typically a missing ")
	sb.WriteString("interface method, an unwired entry point, an import mismatch, or a composition root that ")
	sb.WriteString("was never assembled. Do not rewrite working code.\n")
	sb.WriteString("3. Run the project's build and test commands and confirm they pass.\n")
	fmt.Fprintf(&sb, "4. Commit the fix with a clear message and push it to the '%s' branch.\n", g.baseBranch)
	sb.WriteString("Do NOT ask clarifying questions. Do NOT produce JSON. Apply the fix directly.")
	return sb.String()
}
