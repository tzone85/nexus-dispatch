package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

const nxdFooter = `

---

<p align="center">
  Built with <strong>NXD (Nexus Dispatch)</strong> — offline-first autonomous AI software delivery
</p>
`

// generateDocumentation creates or updates the README.md in the target repo
// after all stories are merged. Uses the LLM to generate documentation based
// on the actual codebase, requirement, and completed stories.
func generateDocumentation(ctx context.Context, repoDir string, reqTitle string, stories []string, client llm.Client, model string) {
	log.Printf("[docs] generating documentation for %s", filepath.Base(repoDir))

	readmePath := filepath.Join(repoDir, "README.md")
	existingReadme, _ := os.ReadFile(readmePath)
	hasReadme := len(existingReadme) > 0

	// Get the file tree for context
	fileTree := captureFileTree(repoDir)

	// Get package.json or go.mod for project info
	projectInfo := ""
	for _, name := range []string{"package.json", "go.mod", "pyproject.toml", "Cargo.toml"} {
		data, err := os.ReadFile(filepath.Join(repoDir, name))
		if err == nil {
			projectInfo = string(data)
			if len(projectInfo) > 2000 {
				projectInfo = projectInfo[:2000]
			}
			break
		}
	}

	var prompt string
	if hasReadme {
		prompt = fmt.Sprintf(`You are updating the README.md for a software project.

EXISTING README:
%s

REQUIREMENT THAT WAS JUST IMPLEMENTED:
%s

STORIES COMPLETED:
%s

FILE TREE:
%s

Instructions:
1. Keep the existing README structure and content
2. Add a new section documenting the features that were just implemented
3. Update any outdated information (version, features list, etc.)
4. Do NOT remove existing content — only add and update
5. Output ONLY the complete updated README.md content, no commentary
6. Do NOT wrap in markdown code fences`,
			truncateForPrompt(string(existingReadme), 4000),
			reqTitle,
			strings.Join(stories, "\n"),
			truncateForPrompt(fileTree, 2000),
		)
	} else {
		prompt = fmt.Sprintf(`Create a professional README.md for this software project.

PROJECT INFO:
%s

REQUIREMENT:
%s

FEATURES IMPLEMENTED:
%s

FILE TREE:
%s

Instructions:
1. Create a complete README with: title, description, features, installation, usage, tech stack, contributing, license
2. Use the file tree and project info to determine the tech stack accurately
3. Make it professional and inviting for open source contributors
4. Output ONLY the README.md content, no commentary
5. Do NOT wrap in markdown code fences`,
			truncateForPrompt(projectInfo, 2000),
			reqTitle,
			strings.Join(stories, "\n"),
			truncateForPrompt(fileTree, 2000),
		)
	}

	// Call LLM to generate/update the README
	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 4000,
	})
	if err != nil {
		log.Printf("[docs] LLM error generating README: %v", err)
		return
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		log.Printf("[docs] LLM returned empty README content, skipping")
		return
	}

	// Strip markdown fences if the LLM wrapped it anyway
	content = stripMarkdownFences(content)

	// Factory rule: ship rendered SVG architecture + sequence diagrams (never
	// Mermaid). Generate/repair them deterministically and link from the README.
	diagrams := generateProjectDiagrams(ctx, repoDir, reqTitle, fileTree, projectInfo, client, model)
	content = ensureReadmeReferencesDiagrams(content, diagrams)

	// Append NXD footer if not already present
	if !strings.Contains(content, "Built by the") && !strings.Contains(content, "NXD Team") {
		content += nxdFooter
	}

	// Write the README
	if err := os.WriteFile(readmePath, []byte(content), 0644); err != nil {
		log.Printf("[docs] failed to write README: %v", err)
		return
	}

	// Factory documentation standard: fill any remaining shortfall — a
	// getting-started training guide and Architecture Decision Records when the
	// agent did not supply them, then a deterministic docs/ index over the lot.
	ensureFactoryDocs(ctx, repoDir, reqTitle, fileTree, projectInfo, client, model)

	// Commit the documentation update (README + generated docs/ diagrams, ADRs,
	// training guide, and index)
	commitDocumentation(repoDir)

	action := "created"
	if hasReadme {
		action = "updated"
	}
	log.Printf("[docs] README.md %s for %s", action, filepath.Base(repoDir))
}

// commitDocumentation stages and commits the README plus generated docs/
// (architecture.svg, sequence.svg, training.md).
func commitDocumentation(repoDir string) {
	addArgs := []string{"add", "README.md"}
	if st, err := os.Stat(filepath.Join(repoDir, "docs")); err == nil && st.IsDir() {
		addArgs = append(addArgs, "docs")
	}
	addCmd := exec.Command("git", addArgs...)
	addCmd.Dir = repoDir
	if err := addCmd.Run(); err != nil {
		log.Printf("[docs] git add %v failed: %v", addArgs, err)
		return
	}

	msg := fmt.Sprintf("docs: update README and SVG diagrams for implemented features\n\nAuto-generated by NXD on %s", time.Now().Format("2006-01-02"))
	commitCmd := exec.Command("git", "commit", "-m", msg)
	commitCmd.Dir = repoDir
	// `git commit` returns non-zero for BOTH "nothing to commit" AND real
	// failures (hook rejection, locked index, GPG signing failure). The
	// old code blindly ignored both. Distinguish: check the porcelain
	// status first; if nothing is staged, return silently. Otherwise the
	// commit failure is real and must be logged so operators see why
	// the README never made it to the remote.
	staged, _ := exec.Command("git", "diff", "--cached", "--quiet").CombinedOutput()
	_ = staged
	stagedCmd := exec.Command("git", "diff", "--cached", "--name-only")
	stagedCmd.Dir = repoDir
	stagedOut, _ := stagedCmd.Output()
	if strings.TrimSpace(string(stagedOut)) == "" {
		// Nothing to commit — README was unchanged. Silent return is fine.
		return
	}
	if out, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("[docs] commit README failed: %v — %s", err, strings.TrimSpace(string(out)))
	}
}

// stripMarkdownFences removes ```markdown ... ``` wrapping from LLM output.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```markdown") {
		s = strings.TrimPrefix(s, "```markdown")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```md") {
		s = strings.TrimPrefix(s, "```md")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSpace(s)
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

// truncateForPrompt limits text length for LLM prompts.
func truncateForPrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...(truncated)"
}
