package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// adrRecord is one Architecture Decision Record as returned by the doc model.
type adrRecord struct {
	Title        string `json:"title"`
	Context      string `json:"context"`
	Decision     string `json:"decision"`
	Consequences string `json:"consequences"`
}

// ensureADRs generates docs/adr/ Architecture Decision Records (and their index)
// when the coding agent did not already supply them. Best-effort: any failure or
// empty/invalid model output logs and returns without writing partial files.
func ensureADRs(ctx context.Context, repoDir, reqTitle, fileTree, projectInfo string, client llm.Client, model string) {
	adrDir := filepath.Join(repoDir, "docs", "adr")
	if hasADRFiles(adrDir) {
		return // agent already wrote ADRs
	}

	prompt := fmt.Sprintf(`Identify the significant, hard-to-reverse ARCHITECTURE DECISIONS in this software project and record them as ADRs, grounded in the actual code (cite real package/module/type names, never invent).

PROJECT: %s

MANIFEST (truncated):
%s

FILE TREE (truncated):
%s

Return ONLY a JSON array (no prose, no code fence) of 3 to 6 objects, each:
{"title": "...", "context": "why this decision was needed", "decision": "what was decided", "consequences": "trade-offs and effects"}

Each must be a REAL decision evident in the structure/stack/patterns (e.g. persistence choice, layering, offline-vs-network, auth model, a key algorithm, an error-handling contract). Title is a short noun phrase. No markdown inside the strings beyond plain sentences.`,
		reqTitle, truncateForPrompt(projectInfo, 1500), truncateForPrompt(fileTree, 1800))

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Model:     model,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens: 4000,
	})
	if err != nil {
		log.Printf("[docs] ADR generation failed: %v", err)
		return
	}

	adrs := parseADRs(resp.Content)
	if len(adrs) == 0 {
		log.Printf("[docs] ADR generation produced no usable records, skipping")
		return
	}

	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		log.Printf("[docs] mkdir docs/adr failed: %v", err)
		return
	}
	for i, a := range adrs {
		num := i + 1
		filename := fmt.Sprintf("%04d-%s.md", num, slugifyADR(a.Title))
		if err := os.WriteFile(filepath.Join(adrDir, filename), []byte(renderADR(num, a)), 0o644); err != nil {
			log.Printf("[docs] write %s failed: %v", filename, err)
		}
	}
	if err := os.WriteFile(filepath.Join(adrDir, "README.md"), []byte(renderADRIndex(adrs)), 0o644); err != nil {
		log.Printf("[docs] write docs/adr/README.md failed: %v", err)
	}
	log.Printf("[docs] generated %d ADR(s) in docs/adr/", len(adrs))
}

// parseADRs extracts and validates the ADR array from a model response. Records
// missing a title or decision are dropped (an ADR with neither is not useful).
func parseADRs(raw string) []adrRecord {
	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return nil
	}
	var records []adrRecord
	if err := json.Unmarshal([]byte(jsonStr), &records); err != nil {
		return nil
	}
	var valid []adrRecord
	for _, r := range records {
		r.Title = strings.TrimSpace(r.Title)
		r.Decision = strings.TrimSpace(r.Decision)
		if r.Title == "" || r.Decision == "" {
			continue
		}
		valid = append(valid, r)
	}
	return valid
}

// renderADR renders one ADR as a Markdown file following the standard sections.
func renderADR(num int, a adrRecord) string {
	field := func(s, fallback string) string {
		if strings.TrimSpace(s) == "" {
			return fallback
		}
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf(`# %d. %s

- Status: Accepted

## Context

%s

## Decision

%s

## Consequences

%s
`,
		num, a.Title,
		field(a.Context, "_Not recorded._"),
		field(a.Decision, "_Not recorded._"),
		field(a.Consequences, "_Not recorded._"))
}

// renderADRIndex renders the docs/adr/README.md index table.
func renderADRIndex(adrs []adrRecord) string {
	var b strings.Builder
	b.WriteString("# Architecture Decision Records\n\n")
	b.WriteString("Significant, hard-to-reverse decisions for this project.\n\n")
	b.WriteString("| ADR | Decision |\n|-----|----------|\n")
	for i, a := range adrs {
		num := i + 1
		link := fmt.Sprintf("%04d-%s.md", num, slugifyADR(a.Title))
		fmt.Fprintf(&b, "| [%04d](%s) | %s |\n", num, link, a.Title)
	}
	return b.String()
}

// hasADRFiles reports whether docs/adr/ already contains at least one numbered
// ADR file (e.g. 0001-*.md), meaning the agent supplied them.
func hasADRFiles(adrDir string) bool {
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && adrFilePattern.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

var adrFilePattern = regexp.MustCompile(`^\d{3,4}-.*\.md$`)

// slugifyADR turns an ADR title into a filename-safe slug.
func slugifyADR(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		return "decision"
	}
	return s
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
