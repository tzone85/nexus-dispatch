package security

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the aggregated outcome of a scan: which scanners ran, which were
// skipped (applicable but not installed), the deduplicated findings, and the
// knowledge-base version that informed the LLM pass.
type Report struct {
	RepoDir     string        `json:"repo_dir"`
	Languages   []string      `json:"languages"`
	ScannersRun []ScannerKind `json:"scanners_run"`
	Skipped     []ScannerKind `json:"skipped"`
	Findings    []Finding     `json:"findings"`
	KBVersion   int           `json:"kb_version"`
}

// Total is the number of findings.
func (r Report) Total() int { return len(r.Findings) }

// Counts tallies findings by severity.
func (r Report) Counts() map[Severity]int {
	c := map[Severity]int{}
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	return c
}

// MaxSeverity returns the highest severity present (Info if no findings).
func (r Report) MaxSeverity() Severity {
	max := SeverityInfo
	for _, f := range r.Findings {
		if f.Severity > max {
			max = f.Severity
		}
	}
	return max
}

// HasAtLeast reports whether any finding is at least the given severity.
func (r Report) HasAtLeast(sev Severity) bool {
	for _, f := range r.Findings {
		if f.Severity.AtLeast(sev) {
			return true
		}
	}
	return false
}

// FormatMarkdown renders an operator-facing summary: a severity tally, the
// scanners that ran/were skipped, and the findings ordered by severity.
func (r Report) FormatMarkdown() string {
	var b strings.Builder
	c := r.Counts()
	fmt.Fprintf(&b, "## Security scan — %s\n\n", r.RepoDir)
	fmt.Fprintf(&b, "Languages: %s · KB v%d\n\n", strings.Join(r.Languages, ", "), r.KBVersion)
	fmt.Fprintf(&b, "Findings: %d total — %d critical, %d high, %d medium, %d low, %d info\n\n",
		r.Total(), c[SeverityCritical], c[SeverityHigh], c[SeverityMedium], c[SeverityLow], c[SeverityInfo])

	run := make([]string, len(r.ScannersRun))
	for i, s := range r.ScannersRun {
		run[i] = string(s)
	}
	fmt.Fprintf(&b, "Scanners run: %s\n", joinOrNone(run))
	skip := make([]string, len(r.Skipped))
	for i, s := range r.Skipped {
		skip[i] = string(s)
	}
	fmt.Fprintf(&b, "Skipped (not installed): %s\n\n", joinOrNone(skip))

	// Findings, most severe first, then by file for stable output.
	sorted := make([]Finding, len(r.Findings))
	copy(sorted, r.Findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		return sorted[i].File < sorted[j].File
	})
	for _, f := range sorted {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&b, "- [%s] %s — %s (%s/%s) %s\n",
			strings.ToUpper(f.Severity.String()), f.Title, loc, f.Tool, f.RuleID, f.Detail)
	}
	return b.String()
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
