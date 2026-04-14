// Package codegraph integrates code-review-graph for structural dependency
// analysis and blast-radius detection. All functions degrade gracefully —
// if the binary or graph DB is unavailable, they return empty results.
package codegraph

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// GraphInfo holds summary statistics about a code graph.
type GraphInfo struct {
	NodeCount   int       `json:"node_count"`
	EdgeCount   int       `json:"edge_count"`
	FileCount   int       `json:"file_count"`
	Languages   []string  `json:"languages"`
	LastUpdated time.Time `json:"last_updated"`
	CommitHash  string    `json:"commit_hash"`
}

// ImpactAnalysis is the result of a blast-radius analysis.
type ImpactAnalysis struct {
	RiskScore        float64       `json:"risk_score"`
	Summary          string        `json:"summary"`
	ChangedFunctions []ChangedNode `json:"changed_functions"`
	TestGaps         []TestGap     `json:"test_gaps"`
	ReviewPriorities []ChangedNode `json:"review_priorities"`
	AffectedFiles    []string      `json:"affected_files"`
}

// ChangedNode is a function/class affected by a change.
type ChangedNode struct {
	Name      string  `json:"name"`
	FilePath  string  `json:"file_path"`
	Kind      string  `json:"kind"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	RiskScore float64 `json:"risk_score"`
	IsTest    bool    `json:"is_test"`
}

// TestGap identifies a changed function without test coverage.
type TestGap struct {
	Name      string `json:"name"`
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

// Empty reports whether the analysis produced no results.
func (ia *ImpactAnalysis) Empty() bool {
	return ia == nil || (len(ia.ChangedFunctions) == 0 && len(ia.TestGaps) == 0 && len(ia.ReviewPriorities) == 0)
}

// FormatMarkdown renders the impact analysis as a markdown section
// suitable for injection into reviewer prompts.
func (ia *ImpactAnalysis) FormatMarkdown() string {
	if ia.Empty() {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Blast Radius Analysis (risk: %.2f/1.0)\n", ia.RiskScore))
	b.WriteString(ia.Summary)
	b.WriteString("\n\n")

	if len(ia.ReviewPriorities) > 0 {
		b.WriteString("### Review Priorities (by risk):\n")
		limit := len(ia.ReviewPriorities)
		if limit > 10 {
			limit = 10
		}
		for i, n := range ia.ReviewPriorities[:limit] {
			b.WriteString(fmt.Sprintf("%d. %s (%s:%d-%d) — risk %.2f\n",
				i+1, n.Name, shortPath(n.FilePath), n.LineStart, n.LineEnd, n.RiskScore))
		}
		b.WriteString("\n")
	}

	if len(ia.TestGaps) > 0 {
		b.WriteString("### Test Gaps:\n")
		limit := len(ia.TestGaps)
		if limit > 10 {
			limit = 10
		}
		for _, g := range ia.TestGaps[:limit] {
			b.WriteString(fmt.Sprintf("- %s (%s:%d-%d) — no test coverage\n",
				g.Name, shortPath(g.FilePath), g.LineStart, g.LineEnd))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// UniqueAffectedFiles extracts deduplicated file paths from review priorities,
// sorted alphabetically.
func (ia *ImpactAnalysis) UniqueAffectedFiles() []string {
	if ia.Empty() {
		return nil
	}
	seen := make(map[string]bool)
	for _, n := range ia.ReviewPriorities {
		if !n.IsTest {
			seen[n.FilePath] = true
		}
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// shortPath trims a file path to its last two segments for readability.
func shortPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}
