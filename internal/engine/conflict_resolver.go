package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ConflictResolver uses an LLM to automatically resolve git merge conflicts
// that occur during rebase. It reads conflicted files, sends them to the LLM
// for resolution, writes the resolved content back, and continues the rebase.
type ConflictResolver struct {
	llmClient  llm.Client
	model      string
	maxTokens  int
	eventStore state.EventStore
	maxRounds  int // max rebase-continue rounds (one per conflicting commit)
}

// NewConflictResolver creates a ConflictResolver with the given LLM client.
func NewConflictResolver(client llm.Client, model string, maxTokens int, es state.EventStore) *ConflictResolver {
	return &ConflictResolver{
		llmClient:  client,
		model:      model,
		maxTokens:  maxTokens,
		eventStore: es,
		maxRounds:  10,
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
			nxdgit.RebaseAbort(worktreePath)
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
				nxdgit.RebaseAbort(worktreePath)
				return contErr
			}
			continue
		}

		log.Printf("[conflict-resolver] round %d: resolving %d conflicted file(s) for %s: %v",
			round+1, len(files), storyID, files)

		// Resolve each conflicted file via LLM.
		for _, file := range files {
			absPath := filepath.Join(worktreePath, file)
			content, rErr := os.ReadFile(absPath)
			if rErr != nil {
				nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("read conflicted file %s: %w", file, rErr)
			}

			resolved, rErr := cr.resolveFile(ctx, file, string(content))
			if rErr != nil {
				nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("LLM resolve %s: %w", file, rErr)
			}

			if wErr := os.WriteFile(absPath, []byte(resolved), 0o644); wErr != nil {
				nxdgit.RebaseAbort(worktreePath)
				return fmt.Errorf("write resolved %s: %w", file, wErr)
			}
		}

		// Stage resolved files and continue rebase.
		if sErr := nxdgit.StageFiles(worktreePath, files); sErr != nil {
			nxdgit.RebaseAbort(worktreePath)
			return fmt.Errorf("stage resolved files: %w", sErr)
		}

		contErr := nxdgit.RebaseContinue(worktreePath)
		if contErr == nil {
			log.Printf("[conflict-resolver] rebase complete for %s after %d resolution round(s)", storyID, round+1)
			cr.emitResolutionEvent(storyID, files, round+1)
			return nil
		}

		if !nxdgit.IsConflict(contErr) {
			nxdgit.RebaseAbort(worktreePath)
			return contErr
		}

		// More conflicts from the next commit in the rebase — loop again.
		log.Printf("[conflict-resolver] additional conflicts in next commit for %s, continuing", storyID)
	}

	nxdgit.RebaseAbort(worktreePath)
	return fmt.Errorf("conflict resolution exhausted after %d rounds", cr.maxRounds)
}

// resolveFile sends a conflicted file to the LLM and returns the resolved content.
func (cr *ConflictResolver) resolveFile(ctx context.Context, filename, conflictedContent string) (string, error) {
	prompt := fmt.Sprintf(`You are resolving a git merge conflict. The file below contains conflict markers (<<<<<<< HEAD, =======, >>>>>>> ...).

Your task:
1. Read both sides of every conflict
2. Produce the CORRECT merged version that preserves ALL functionality from BOTH sides
3. Remove ALL conflict markers
4. Return ONLY the resolved file content — no explanations, no markdown fences

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
		return "", err
	}

	resolved := resp.Content

	// Strip markdown fences if the LLM wrapped the output.
	resolved = stripCodeFences(resolved)

	// Sanity check: resolved content must not contain conflict markers.
	if strings.Contains(resolved, "<<<<<<<") || strings.Contains(resolved, ">>>>>>>") {
		return "", fmt.Errorf("LLM output still contains conflict markers")
	}

	return resolved, nil
}

// stripCodeFences removes leading/trailing markdown code fences from LLM output.
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
		cr.eventStore.Append(evt)
	}
}
