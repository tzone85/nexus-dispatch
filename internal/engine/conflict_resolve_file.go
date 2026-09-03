package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// Sentinels the resolver asks the model to wrap the resolved file in. Unlike
// triple backticks they cannot collide with fences inside Markdown or with
// code that contains ``` literals, so the whole file survives extraction.
const (
	resolvedFileSentinelStart = "<<<NXD_RESOLVED_FILE>>>"
	resolvedFileSentinelEnd   = "<<<END_NXD_RESOLVED_FILE>>>"
)

// minResolvedSizeRatio is the smallest acceptable ratio of resolved size to
// the conflicted input's payload size (input minus the marker lines). A
// correct merge keeps (nearly) everything from both sides, so an output that
// is less than half the payload almost certainly dropped content and must not
// be written.
const minResolvedSizeRatio = 0.5

// conflictReasonFileTooLarge is the human-readable reason recorded on the
// STORY_CONFLICT_ESCALATED event when a conflicted file exceeds
// maxConflictContentBytes and is left for a human.
const conflictReasonFileTooLarge = "file too large for automatic resolution"

// ConflictTooLargeError reports a conflicted file that exceeds the size the
// resolver is willing to hand to an LLM. The rebase is left for a human — the
// file is never truncated, never sent, never overwritten.
type ConflictTooLargeError struct {
	File  string
	Size  int
	Limit int
}

func (e *ConflictTooLargeError) Error() string {
	return fmt.Sprintf("conflict in %s: %s (%d bytes > %d byte limit)", e.File, conflictReasonFileTooLarge, e.Size, e.Limit)
}

// ConflictEscalatedError reports a text conflict the LLM could not resolve
// safely (model failure or an output that failed validation). The file on
// disk is untouched and a STORY_CONFLICT_ESCALATED event has been emitted.
type ConflictEscalatedError struct {
	File   string
	Reason string
	Err    error // underlying cause, may be nil
}

func (e *ConflictEscalatedError) Error() string {
	return fmt.Sprintf("conflict in %s escalated: %s", e.File, e.Reason)
}

// Unwrap exposes the underlying cause so llm.IsFatalAPIError and friends keep
// working through the escalation wrapper.
func (e *ConflictEscalatedError) Unwrap() error { return e.Err }

// resolveTextConflict resolves one conflicted text file in place. It reads the
// file, refuses oversized content (escalating instead of truncating), asks the
// senior model (and the Tech Lead when configured and needed), validates the
// model's output, and only then writes it back. Any failure leaves the file
// exactly as git left it and returns a typed error.
func (cr *ConflictResolver) resolveTextConflict(ctx context.Context, storyID, worktreePath, file string, needsTechLead bool) error {
	absPath := filepath.Join(worktreePath, file)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read conflicted file %s: %w", file, err)
	}
	if len(content) > maxConflictContentBytes {
		log.Printf("[conflict-resolver] %s in %s is %d bytes (> %d): escalating to human, not truncating",
			file, storyID, len(content), maxConflictContentBytes)
		cr.emitEscalationEventWithReason(storyID, file, "file_too_large", conflictReasonFileTooLarge)
		return &ConflictTooLargeError{File: file, Size: len(content), Limit: maxConflictContentBytes}
	}
	original := string(content)

	resolved, seniorErr := cr.resolveFile(ctx, file, original)

	if seniorErr != nil || needsTechLead {
		switch {
		case cr.techLeadClient != nil:
			tlCtx := cr.buildTechLeadContext(ctx, storyID, worktreePath, file)
			var tlErr error
			resolved, tlErr = cr.resolveFileTechLead(ctx, file, original, tlCtx)
			if tlErr != nil {
				cr.emitEscalationEventWithReason(storyID, file, "tech_lead_failed", tlErr.Error())
				if llm.IsFatalAPIError(tlErr) {
					log.Printf("[conflict-resolver] FATAL: Tech Lead error for %s: %v", storyID, tlErr)
				}
				return &ConflictEscalatedError{File: file, Reason: tlErr.Error(), Err: tlErr}
			}
			cr.emitEscalationEvent(storyID, file, "tech_lead_resolved")
		case seniorErr != nil:
			cr.emitEscalationEventWithReason(storyID, file, "senior_failed", seniorErr.Error())
			if llm.IsFatalAPIError(seniorErr) {
				log.Printf("[conflict-resolver] FATAL: API error during conflict resolution for %s: %v", storyID, seniorErr)
			}
			return &ConflictEscalatedError{File: file, Reason: seniorErr.Error(), Err: seniorErr}
		}
		// needsTechLead but no Tech Lead configured and senior succeeded: keep the senior result.
	}

	if err := os.WriteFile(absPath, []byte(resolved), 0o644); err != nil {
		return fmt.Errorf("write resolved %s: %w", file, err)
	}
	return nil
}

// completeResolution runs one resolution prompt against client, extracts the
// file from the reply and validates it against the conflicted input.
func (cr *ConflictResolver) completeResolution(ctx context.Context, client llm.Client, model, who, prompt, original string) (string, error) {
	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Model:       model,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: prompt}},
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
	if err := validateResolvedContent(original, resolved); err != nil {
		return "", fmt.Errorf("%s output rejected: %w", who, err)
	}
	return resolved, nil
}

// resolutionOutputRules is the shared output contract appended to every
// resolution prompt.
const resolutionOutputRules = `CRITICAL OUTPUT RULES:
- Return the COMPLETE resolved file — every line, from the first to the last
- Wrap the file EXACTLY like this, with the markers on their own lines:
` + resolvedFileSentinelStart + `
<full file content>
` + resolvedFileSentinelEnd + `
- Do NOT use markdown code fences (` + "```" + `) around the file — the file itself may legitimately contain them
- Do NOT add any explanation, preamble, or commentary
- Remove ALL conflict markers (<<<<<<<, =======, >>>>>>>)`

// validateResolvedContent rejects model output that would corrupt the file if
// written: empty output, leftover conflict markers, conversational chatter, or
// a result far smaller than the conflicted input (dropped content).
func validateResolvedContent(original, resolved string) error {
	if strings.TrimSpace(resolved) == "" {
		return fmt.Errorf("resolved content is empty")
	}
	if hasConflictMarkers(resolved) {
		return fmt.Errorf("output still contains conflict markers")
	}
	if looksLikeResolverChatter(resolved) {
		return fmt.Errorf("output is commentary, not file content")
	}
	payload := conflictPayloadSize(original)
	if float64(len(resolved)) < float64(payload)*minResolvedSizeRatio {
		return fmt.Errorf("output shrank to %d bytes from %d (below %.0f%% of input)",
			len(resolved), payload, minResolvedSizeRatio*100)
	}
	return nil
}

// conflictPayloadSize returns the byte size of s without its conflict-marker
// lines, i.e. the content a correct resolution has to preserve.
func conflictPayloadSize(s string) int {
	size := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, "=======") || strings.HasPrefix(line, ">>>>>>>") {
			continue
		}
		size += len(line)
	}
	return size
}

// hasConflictMarkers reports whether s contains a git conflict start/end
// marker at the beginning of a line.
func hasConflictMarkers(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, ">>>>>>>") {
			return true
		}
	}
	return false
}

// extractResolvedFileContent pulls the resolved file out of an LLM response.
// Preferred: the content between the NXD sentinels (fence-safe). Fallback:
// the LARGEST ```fenced block — conflict-resolution models sometimes wrap the
// file in a fence with chatter around it, and a small fence (e.g. a JSON
// snippet in the commentary) must never win over the file. Last resort: the
// trimmed response with any leading/trailing fence stripped.
func extractResolvedFileContent(resp string) string {
	if start := strings.Index(resp, resolvedFileSentinelStart); start >= 0 {
		rest := resp[start+len(resolvedFileSentinelStart):]
		if end := strings.Index(rest, resolvedFileSentinelEnd); end >= 0 {
			return trimSentinelBody(rest[:end])
		}
	}
	if block, ok := largestFencedBlock(resp); ok {
		return block
	}
	return strings.TrimSpace(stripCodeFences(resp))
}

// trimSentinelBody strips the single newline that follows the start sentinel
// and precedes the end sentinel without disturbing the file's own whitespace.
func trimSentinelBody(body string) string {
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimSuffix(body, "\n")
	return strings.TrimSuffix(body, "\r")
}

// largestFencedBlock returns the body of the largest complete ```fenced block
// in resp, or ok=false when there is no complete fenced block.
func largestFencedBlock(resp string) (string, bool) {
	var best string
	found := false
	rest := resp
	for {
		i := strings.Index(rest, "```")
		if i < 0 {
			break
		}
		body := rest[i+3:]
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			body = body[nl+1:]
		} else {
			break
		}
		j := strings.Index(body, "```")
		if j < 0 {
			break
		}
		block := strings.TrimSpace(body[:j])
		if !found || len(block) > len(best) {
			best, found = block, true
		}
		rest = body[j+3:]
	}
	return best, found
}
