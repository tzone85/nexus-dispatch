package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// oversizedBinaryPattern matches compiled binary names that should be removed
// rather than kept when they appear as merge conflicts.
var oversizedBinaryPattern = regexp.MustCompile(`(?i)(^|/)(server|main|app|binary|\.exe)$`)

// maxBinaryKeepBytes is the file-size limit above which a binary conflict file
// is removed (git rm) rather than resolved with --ours.
const maxBinaryKeepBytes = 500 * 1024 // 500 KB

// maxConflictContentBytes is the maximum size of conflicted file content sent
// to an Ollama LLM. Ollama models typically have smaller context windows than
// cloud models, so we truncate to avoid "context length exceeded" errors.
const maxConflictContentBytes = 24 * 1024 // 24 KB

// techLeadContext carries the requirement/story context that the Tech Lead
// resolver includes in its resolution prompt.
type techLeadContext struct {
	requirementTitle   string
	requirementText    string
	storyTitle         string
	storyAcceptance    string
	siblingStoryTitles []string // other stories in the same requirement
	fileHistory        []string // last 3 commit subjects that touched this file
}

// ConflictResolver uses an LLM to automatically resolve git merge conflicts
// that occur during rebase. It reads conflicted files, sends them to the LLM
// for resolution, writes the resolved content back, and continues the rebase.
//
// Resolution strategy (in order):
//  1. Binary file: deterministic policy (--ours or git rm), no LLM call.
//  2. Senior LLM (fast path): resolves simple text conflicts.
//  3. Tech Lead LLM (escalation): richer prompt with requirement/story context,
//     triggered when (a) senior fails, (b) conflict spans >3 files.
type ConflictResolver struct {
	llmClient  llm.Client
	model      string
	maxTokens  int
	eventStore state.EventStore
	maxRounds  int // max rebase-continue rounds (one per conflicting commit)

	// Tech Lead escalation (optional — nil disables escalation).
	techLeadClient llm.Client
	techLeadModel  string
	projStore      state.ProjectionStore
}

// NewConflictResolver creates a ConflictResolver with the given senior LLM client.
// This is the minimal constructor used by existing call sites.
func NewConflictResolver(client llm.Client, model string, maxTokens int, es state.EventStore) *ConflictResolver {
	return &ConflictResolver{
		llmClient:  client,
		model:      model,
		maxTokens:  maxTokens,
		eventStore: es,
		maxRounds:  10,
	}
}

// NewConflictResolverWithTechLead creates a ConflictResolver with both a senior
// and a Tech Lead LLM client. The Tech Lead is used for escalation when the
// senior fails or when >3 files conflict simultaneously.
//
// senior/seniorModel: LLM client and model for fast-path text conflict resolution.
// techLead/techLeadModel: LLM client and model for escalated resolution with full
// requirement context. Pass nil to disable Tech Lead escalation.
// projStore: projection store for fetching requirement/story context. May be nil
// when techLead is also nil.
func NewConflictResolverWithTechLead(
	senior llm.Client, seniorModel string,
	techLead llm.Client, techLeadModel string,
	maxTokens int,
	projStore state.ProjectionStore,
	es state.EventStore,
) *ConflictResolver {
	return &ConflictResolver{
		llmClient:      senior,
		model:          seniorModel,
		techLeadClient: techLead,
		techLeadModel:  techLeadModel,
		maxTokens:      maxTokens,
		projStore:      projStore,
		eventStore:     es,
		maxRounds:      10,
	}
}

// RebaseWithResolution performs a rebase onto upstream, automatically resolving
// any conflicts using the LLM. Returns nil on success. On unresolvable
// conflicts (after maxRounds), aborts the rebase and returns an error.
func (cr *ConflictResolver) RebaseWithResolution(ctx context.Context, storyID, worktreePath, upstream string) error {
	err := nxdgit.StartRebase(worktreePath, upstream)
	if err == nil {
		return nil // clean rebase, no conflicts
	}

	if !nxdgit.IsConflict(err) {
		return err // non-conflict error, already aborted
	}

	log.Printf("[conflict-resolver] rebase conflict detected for %s, attempting auto-resolution", storyID)

	for round := 0; round < cr.maxRounds; round++ {
		files, fErr := nxdgit.ConflictedFiles(worktreePath)
		if fErr != nil {
			_ = nxdgit.RebaseAbort(worktreePath)
			return fmt.Errorf("list conflicted files: %w", fErr)
		}

		if len(files) == 0 {
			// No conflicted files — try continuing.
			contErr := nxdgit.RebaseContinue(worktreePath)
			if contErr == nil {
				log.Printf("[conflict-resolver] rebase complete for %s after %d resolution round(s)", storyID, round+1)
				return nil
			}
			if !nxdgit.IsConflict(contErr) {
				_ = nxdgit.RebaseAbort(worktreePath)
				return contErr
			}
			continue
		}

		log.Printf("[conflict-resolver] round %d: resolving %d conflicted file(s) for %s: %v",
			round+1, len(files), storyID, files)

		// Detect if Tech Lead escalation is needed based on conflict breadth.
		needsTechLead := len(files) > 3

		// Resolve each conflicted file.
		for _, file := range files {
			absPath := filepath.Join(worktreePath, file)

			// Binary-file check: skip LLM entirely.
			isBin, _ := nxdgit.IsBinaryConflict(worktreePath, file)
			if isBin {
				if rErr := cr.handleBinaryConflict(storyID, worktreePath, absPath, file); rErr != nil {
					_ = nxdgit.RebaseAbort(worktreePath)
					return rErr
				}
				continue
			}

			// Generated lock-file check: resolve deterministically (story branch
			// version via --ours, staged by the bulk StageFiles below). Lock files
			// like package-lock.json are huge and machine-generated — sending them
			// to the LLM blows the pipeline timeout for no benefit; the post-merge
			// build/QA validates dependencies.
			if isGeneratedLockFile(file) {
				log.Printf("[conflict-resolver] deterministic resolve (--ours) for generated lock file %s in %s", file, storyID)
				cmd := exec.Command("git", "checkout", "--ours", "--", file)
				cmd.Dir = worktreePath
				if out, err := cmd.CombinedOutput(); err != nil {
					_ = nxdgit.RebaseAbort(worktreePath)
					return fmt.Errorf("git checkout --ours %s: %w (%s)", file, err, strings.TrimSpace(string(out)))
				}
				cr.emitEscalationEvent(storyID, file, "lock_file_deterministic")
				continue
			}

			content, rErr := os.ReadFile(absPath)
			if rErr != nil {
				_ = nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("read conflicted file %s: %w", file, rErr)
			}

			// Truncate oversized content to stay within Ollama context limits.
			contentStr := truncateConflictContent(string(content))

			// Try senior resolver first (fast path).
			resolved, seniorErr := cr.resolveFile(ctx, file, contentStr)

			// Escalate to Tech Lead if:
			//  - senior failed entirely, OR
			//  - this round involves many files (integration-level conflict).
			if seniorErr != nil || needsTechLead {
				if cr.techLeadClient != nil {
					tlCtx := cr.buildTechLeadContext(ctx, storyID, worktreePath, file)
					resolved, rErr = cr.resolveFileTechLead(ctx, file, contentStr, tlCtx)
					if rErr != nil {
						cr.emitEscalationEvent(storyID, file, "tech_lead_failed")
						_ = nxdgit.RebaseAbort(worktreePath)
						if llm.IsFatalAPIError(rErr) {
							log.Printf("[conflict-resolver] FATAL: Tech Lead error for %s: %v", storyID, rErr)
						}
						return fmt.Errorf("tech lead resolve %s: %w", file, rErr)
					}
					cr.emitEscalationEvent(storyID, file, "tech_lead_resolved")
				} else if seniorErr != nil {
					// No tech lead available and senior failed.
					_ = nxdgit.RebaseAbort(worktreePath)
					if llm.IsFatalAPIError(seniorErr) {
						log.Printf("[conflict-resolver] FATAL: API error during conflict resolution for %s: %v", storyID, seniorErr)
					}
					return fmt.Errorf("LLM resolve %s: %w", file, seniorErr)
				}
				// If needsTechLead but senior succeeded and no tech lead: use senior result.
			} else if seniorErr != nil {
				_ = nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("LLM resolve %s: %w", file, seniorErr)
			}

			if wErr := os.WriteFile(absPath, []byte(resolved), 0o644); wErr != nil {
				_ = nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("write resolved %s: %w", file, wErr)
			}
		}

		// Stage resolved files and continue rebase.
		// Re-list conflicted files because binary removals (git rm) shrink the set.
		// Only stage files that are still unresolved — git rm'd files are already
		// staged by git rm itself and must NOT be re-staged.
		remainingFiles, _ := nxdgit.ConflictedFiles(worktreePath)
		if len(remainingFiles) > 0 {
			if sErr := nxdgit.StageFiles(worktreePath, remainingFiles); sErr != nil {
				_ = nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("stage resolved files: %w", sErr)
			}
		}

		contErr := nxdgit.RebaseContinue(worktreePath)
		if contErr == nil {
			log.Printf("[conflict-resolver] rebase complete for %s after %d resolution round(s)", storyID, round+1)
			cr.emitResolutionEvent(storyID, files, round+1)
			return nil
		}

		if !nxdgit.IsConflict(contErr) {
			_ = nxdgit.RebaseAbort(worktreePath)
			return contErr
		}

		// More conflicts from the next commit in the rebase — loop again.
		log.Printf("[conflict-resolver] additional conflicts in next commit for %s, continuing", storyID)
	}

	_ = nxdgit.RebaseAbort(worktreePath)
	return fmt.Errorf("conflict resolution exhausted after %d rounds", cr.maxRounds)
}

// generatedLockFiles are package-manager lock files that are machine-generated
// and must never be LLM-resolved — a conflict is resolved by taking one side
// and letting the build regenerate.
var generatedLockFiles = map[string]bool{
	"package-lock.json":   true,
	"npm-shrinkwrap.json": true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":      true,
	"go.sum":              true,
	"Cargo.lock":          true,
	"composer.lock":       true,
	"Gemfile.lock":        true,
	"poetry.lock":         true,
	"Pipfile.lock":        true,
}

// isGeneratedLockFile reports whether the path's base name is a known
// machine-generated dependency lock file.
func isGeneratedLockFile(path string) bool {
	return generatedLockFiles[filepath.Base(path)]
}

// handleBinaryConflict applies a deterministic policy for binary-file conflicts
// without invoking the LLM:
//   - Oversized (>500 KB) or compiled binary names (server, main, *.exe) → git rm
//   - All others → git checkout --ours (story branch's version wins)
func (cr *ConflictResolver) handleBinaryConflict(storyID, worktreePath, absPath, file string) error {
	info, statErr := os.Stat(absPath)
	isOversized := statErr == nil && info.Size() > maxBinaryKeepBytes
	isCompiled := oversizedBinaryPattern.MatchString(file)

	if isOversized || isCompiled {
		log.Printf("[conflict-resolver] removing oversized/compiled binary %s for %s", file, storyID)
		cmd := exec.Command("git", "rm", "-f", "--", file)
		cmd.Dir = worktreePath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git rm %s: %w (%s)", file, err, strings.TrimSpace(string(out)))
		}
		cr.emitBinaryEvent(storyID, file, state.EventStoryConflictBinaryRemoved,
			"binary removed (oversized or compiled artifact)")
		return nil
	}

	// Take the story branch version (--ours during rebase = the branch being rebased).
	log.Printf("[conflict-resolver] taking --ours for binary %s in %s", file, storyID)
	cmd := exec.Command("git", "checkout", "--ours", "--", file)
	cmd.Dir = worktreePath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout --ours %s: %w (%s)", file, err, strings.TrimSpace(string(out)))
	}
	cr.emitBinaryEvent(storyID, file, state.EventStoryConflictBinary,
		"binary conflict: took --ours (story branch version)")
	return nil
}

// resolveFile sends a conflicted file to the senior LLM and returns the resolved content.
// The prompt explicitly instructs the model not to wrap output in markdown fences,
// which is important for Ollama models that tend to be verbose.
func (cr *ConflictResolver) resolveFile(ctx context.Context, filename, conflictedContent string) (string, error) {
	if cr.llmClient == nil {
		return "", fmt.Errorf("no senior LLM client configured")
	}
	prompt := fmt.Sprintf(`You are resolving a git merge conflict. The file below contains conflict markers (<<<<<<< HEAD, =======, >>>>>>> ...).

Your task:
1. Read both sides of every conflict
2. Produce the CORRECT merged version that preserves ALL functionality from BOTH sides
3. Remove ALL conflict markers
4. Return ONLY the resolved file content

CRITICAL OUTPUT RULES:
- Do NOT wrap your response in markdown code fences (no `+"```"+` blocks)
- Do NOT add any explanation, preamble, or commentary
- Do NOT add "Here is the resolved file:" or similar
- Your entire response must be the file content only, starting at line 1

Key rules:
- Keep ALL additions from both sides (imports, functions, config entries, etc.)
- Maintain correct syntax for the file type
- Preserve the original formatting and style
- If both sides modified the same line differently, combine them logically

File: %s

%s`, filename, conflictedContent)

	resp, err := cr.llmClient.Complete(ctx, llm.CompletionRequest{
		Model: cr.model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   cr.maxTokens,
		Temperature: 0.0,
	})
	if err != nil {
		if llm.IsFatalAPIError(err) {
			return "", fmt.Errorf("fatal API error (credits exhausted or auth failure): %w", err)
		}
		return "", err
	}

	resolved := extractResolvedFileContent(resp.Content)

	// Sanity check: resolved content must not contain conflict markers.
	if strings.Contains(resolved, "<<<<<<<") || strings.Contains(resolved, ">>>>>>>") {
		return "", fmt.Errorf("LLM output still contains conflict markers")
	}

	// Reject conversational commentary. When the model returns prose with no
	// fenced block, writing it would destroy the file; failing here escalates.
	if looksLikeResolverChatter(resolved) {
		return "", fmt.Errorf("LLM returned commentary, not file content")
	}

	return resolved, nil
}

// resolveFileTechLead sends a conflicted file to the Tech Lead LLM with full
// requirement/story context and returns the resolved content.
func (cr *ConflictResolver) resolveFileTechLead(ctx context.Context, filename, conflictedContent string, tlCtx techLeadContext) (string, error) {
	if cr.techLeadClient == nil {
		return "", fmt.Errorf("no Tech Lead LLM client configured")
	}

	siblingStr := "none"
	if len(tlCtx.siblingStoryTitles) > 0 {
		siblingStr = strings.Join(tlCtx.siblingStoryTitles, ", ")
	}
	historyStr := "none"
	if len(tlCtx.fileHistory) > 0 {
		historyStr = strings.Join(tlCtx.fileHistory, "\n  ")
	}

	prompt := fmt.Sprintf(`You are the Tech Lead for requirement: %s
Original requirement:
%s

You're resolving a merge conflict in story: %s
This story's acceptance criteria:
%s

Sibling stories in the same requirement: %s

The conflict is in file: %s
Recent commits to this file:
  %s

Conflict content (with markers):
%s

Resolve the conflict to keep ALL functionality from BOTH sides that is
consistent with the requirement above. Maintain syntax.

CRITICAL OUTPUT RULES:
- Return ONLY the resolved file content
- Do NOT wrap your response in markdown code fences (no `+"```"+` blocks)
- Do NOT add any explanation or commentary
- Start your response with the first line of the resolved file`,
		tlCtx.requirementTitle,
		tlCtx.requirementText,
		tlCtx.storyTitle,
		tlCtx.storyAcceptance,
		siblingStr,
		filename,
		historyStr,
		conflictedContent,
	)

	resp, err := cr.techLeadClient.Complete(ctx, llm.CompletionRequest{
		Model: cr.techLeadModel,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   cr.maxTokens,
		Temperature: 0.0,
	})
	if err != nil {
		if llm.IsFatalAPIError(err) {
			return "", fmt.Errorf("fatal API error (credits exhausted or auth failure): %w", err)
		}
		return "", err
	}

	resolved := extractResolvedFileContent(resp.Content)

	if strings.Contains(resolved, "<<<<<<<") || strings.Contains(resolved, ">>>>>>>") {
		return "", fmt.Errorf("tech lead output still contains conflict markers")
	}

	if looksLikeResolverChatter(resolved) {
		return "", fmt.Errorf("tech lead returned commentary, not file content")
	}

	return resolved, nil
}

// buildTechLeadContext populates a techLeadContext from the projection store and
// git history for the given story and file.
func (cr *ConflictResolver) buildTechLeadContext(ctx context.Context, storyID, worktreePath, file string) techLeadContext {
	_ = ctx // reserved for future async lookups
	tlCtx := techLeadContext{}

	if cr.projStore == nil {
		return tlCtx
	}

	story, err := cr.projStore.GetStory(storyID)
	if err != nil {
		return tlCtx
	}
	tlCtx.storyTitle = story.Title
	tlCtx.storyAcceptance = story.AcceptanceCriteria

	req, err := cr.projStore.GetRequirement(story.ReqID)
	if err == nil {
		tlCtx.requirementTitle = req.Title
		tlCtx.requirementText = req.Description
	}

	// Sibling story titles (same requirement, different story).
	allStories, lErr := cr.projStore.ListStories(state.StoryFilter{ReqID: story.ReqID})
	if lErr == nil {
		for _, s := range allStories {
			if s.ID != storyID {
				tlCtx.siblingStoryTitles = append(tlCtx.siblingStoryTitles, s.Title)
			}
		}
	}

	// Recent git commit subjects for this file.
	tlCtx.fileHistory = gitFileHistory(worktreePath, file, 3)

	return tlCtx
}

// gitFileHistory returns the last n commit subjects that touched the given file.
func gitFileHistory(worktreePath, file string, n int) []string {
	cmd := exec.Command("git", "log", fmt.Sprintf("--pretty=%%s"), fmt.Sprintf("-%d", n), "--", file)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	var subjects []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects
}

// truncateConflictContent truncates conflicted file content to maxConflictContentBytes
// so Ollama models with smaller context windows don't receive oversized prompts.
// If truncation occurs, a warning line is appended.
func truncateConflictContent(content string) string {
	if len(content) <= maxConflictContentBytes {
		return content
	}
	return content[:maxConflictContentBytes] + "\n... [content truncated to fit context window]"
}

// stripCodeFences removes leading/trailing markdown code fences from LLM output.
// extractResolvedFileContent pulls the resolved file out of an LLM response.
// Conflict-resolution models sometimes wrap the file in a ```fenced block with
// conversational preamble/postamble ("Resolved. Kept X ... File content to
// apply: ```json {…}``` Grant write to apply."). Writing that whole reply
// verbatim corrupts the file (it broke a real build's package.json into invalid
// JSON). When a fenced block is present return ONLY its contents; otherwise
// fall back to trimming stray fences.
func extractResolvedFileContent(resp string) string {
	if i := strings.Index(resp, "```"); i >= 0 {
		rest := resp[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return strings.TrimSpace(stripCodeFences(resp))
}

// resolverChatterMarkers are phrases that appear in a conflict-resolution model's
// CONVERSATIONAL reply but should never appear in actual merged source. When the
// model ignores the "return only file content" instruction and emits prose with
// NO fenced block (so extractResolvedFileContent has nothing to extract and
// returns the prose itself), writing that result DESTROYS the file. These markers
// are specific enough that real code/comments don't trip them.
var resolverChatterMarkers = []string{
	"conflict resolved",
	"resolved content",
	"resolved file content",
	"resolved content below",
	"resolved content:",
	"kept both sides",
	"both sides merged",
	"kept head's",
	"kept incoming",
	"write blocked on permission",
	"blocked on permission",
	"permission denied by harness",
	"cannot write the file",
	"can't write the file",
	"i cannot write",
	"i can't write",
	"can't write it here",
	"want me to apply",
	"want me to run",
	"grant write to apply",
	"working tree is",
	"returning the resolved content",
	"apply it to",
	"apply this on branch",
	"all functionality retained",
	"no selector collisions",
	"separate class namespaces",
}

// looksLikeResolverChatter reports whether s is conflict-resolver commentary
// rather than merged file content. It is intentionally conservative: a single
// high-confidence marker is enough, because these phrases do not occur in valid
// source files, and writing chatter to disk corrupts the file irrecoverably.
func looksLikeResolverChatter(s string) bool {
	lower := strings.ToLower(s)
	for _, m := range resolverChatterMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove first line (```lang) and last line (```)
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 {
			start := 1
			end := len(lines)
			if strings.TrimSpace(lines[end-1]) == "```" {
				end--
			}
			s = strings.Join(lines[start:end], "\n")
		}
	}
	return s
}

func (cr *ConflictResolver) emitResolutionEvent(storyID string, files []string, rounds int) {
	evt := state.NewEvent(state.EventStoryProgress, "conflict-resolver", storyID, map[string]any{
		"action": "conflicts_resolved",
		"files":  files,
		"rounds": rounds,
	})
	if cr.eventStore != nil {
		_ = cr.eventStore.Append(evt)
	}
}

func (cr *ConflictResolver) emitBinaryEvent(storyID, file string, eventType state.EventType, reason string) {
	evt := state.NewEvent(eventType, "conflict-resolver", storyID, map[string]any{
		"file":   file,
		"reason": reason,
	})
	if cr.eventStore != nil {
		_ = cr.eventStore.Append(evt)
	}
}

func (cr *ConflictResolver) emitEscalationEvent(storyID, file, outcome string) {
	evt := state.NewEvent(state.EventStoryConflictEscalated, "conflict-resolver", storyID, map[string]any{
		"file":    file,
		"outcome": outcome,
	})
	if cr.eventStore != nil {
		_ = cr.eventStore.Append(evt)
	}
}
