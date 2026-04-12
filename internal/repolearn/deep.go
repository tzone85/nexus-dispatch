package repolearn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// maxFileBytes is the maximum size of a file to read for LLM analysis.
// Larger files are truncated to avoid blowing the context window.
const maxFileBytes = 8000

// ScanDeep performs Pass 3: LLM-assisted deep analysis.
// It reads key files (README, main entry point, test examples) and asks
// the LLM to synthesise a brief project summary and architectural notes.
// Results are stored as Signal entries with Kind="llm_summary".
//
// If client is nil, the pass is skipped gracefully (no error returned).
func ScanDeep(ctx context.Context, profile *RepoProfile, client llm.Client, model string) error {
	if client == nil {
		return nil
	}

	// Gather context from key files
	fileContents := gatherKeyFiles(profile)
	if fileContents == "" {
		// Nothing to analyse — mark pass as done but don't call LLM
		profile.MarkPass(3)
		return nil
	}

	// Build the prompt
	systemPrompt := `You are an expert software architect. Analyse the following repository files and produce a concise summary.

Output format (plain text, no markdown headers):
1. PROJECT PURPOSE: One sentence describing what this project does.
2. ARCHITECTURE: 2-3 sentences on the overall architecture pattern (e.g. MVC, event-sourced, microservices).
3. KEY PATTERNS: List 3-5 important patterns or conventions used (e.g. "dependency injection via constructors", "table-driven tests").
4. GOTCHAS: Any non-obvious things a new contributor should know (0-3 items).

Be specific to THIS codebase. Do not give generic advice.`

	userMessage := fmt.Sprintf("Repository: %s\nTech stack: %s %s\n\n%s",
		filepath.Base(profile.RepoPath),
		profile.TechStack.PrimaryLanguage,
		profile.TechStack.PrimaryFramework,
		fileContents,
	)

	maxTokens := 1000
	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: userMessage}},
	})
	if err != nil {
		// LLM failure is non-fatal — log it as a signal and continue
		profile.AddSignal("llm_error", fmt.Sprintf("Deep analysis failed: %v", err), "")
		profile.MarkPass(3)
		return nil
	}

	summary := strings.TrimSpace(resp.Content)
	if summary != "" {
		// Replace any existing LLM summary (re-runs overwrite)
		filtered := profile.Signals[:0]
		for _, s := range profile.Signals {
			if s.Kind != "llm_summary" {
				filtered = append(filtered, s)
			}
		}
		profile.Signals = filtered
		profile.Signals = append(profile.Signals, Signal{
			Kind:    "llm_summary",
			Message: summary,
		})
	}

	profile.MarkPass(3)
	return nil
}

// gatherKeyFiles reads and concatenates the most informative files in the repo.
// Each file is prefixed with its path for context.
func gatherKeyFiles(profile *RepoProfile) string {
	repoPath := profile.RepoPath
	var parts []string

	// README — most important
	for _, name := range []string{"README.md", "README.rst", "README.txt", "README"} {
		if content := readFileTruncated(filepath.Join(repoPath, name), maxFileBytes); content != "" {
			parts = append(parts, fmt.Sprintf("=== %s ===\n%s", name, content))
			break
		}
	}

	// Main entry point(s)
	for _, ep := range profile.Structure.EntryPoints {
		if len(parts) >= 4 {
			break // limit total files to keep context manageable
		}
		content := readFileTruncated(filepath.Join(repoPath, ep.Path), maxFileBytes)
		if content != "" {
			parts = append(parts, fmt.Sprintf("=== %s ===\n%s", ep.Path, content))
		}
	}

	// One test file example (to understand test conventions)
	if profile.Test.TestFilePattern != "" {
		testFile := findFirstMatchingFile(repoPath, profile.Test.TestFilePattern)
		if testFile != "" {
			rel, _ := filepath.Rel(repoPath, testFile)
			content := readFileTruncated(testFile, maxFileBytes)
			if content != "" {
				parts = append(parts, fmt.Sprintf("=== %s ===\n%s", rel, content))
			}
		}
	}

	// Config file (if it exists)
	for _, name := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		content := readFileTruncated(filepath.Join(repoPath, name), maxFileBytes)
		if content != "" {
			parts = append(parts, fmt.Sprintf("=== %s ===\n%s", name, content))
			break
		}
	}

	return strings.Join(parts, "\n\n")
}

// readFileTruncated reads a file up to maxBytes. Returns empty string on error.
func readFileTruncated(path string, maxBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > maxBytes {
		data = data[:maxBytes]
	}
	return string(data)
}

// findFirstMatchingFile walks the repo looking for the first file matching
// the given glob pattern. Skips hidden and vendor directories.
func findFirstMatchingFile(repoPath, pattern string) string {
	var found string
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		matched, _ := filepath.Match(pattern, info.Name())
		if matched {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
