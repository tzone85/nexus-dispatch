package security

import "fmt"

// Finding is a single security issue surfaced by a scanner or the LLM review.
type Finding struct {
	Tool     string   `json:"tool"`     // "gosec", "semgrep", "govulncheck", "gitleaks", "npm-audit", "llm"
	RuleID   string   `json:"rule_id"`  // tool rule id, CWE, or OWASP category
	Severity Severity `json:"severity"` // serialised as its int rank
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Category string   `json:"category,omitempty"` // OWASP category when known
	Source   string   `json:"source"`             // "scanner" | "llm"
}

// key identifies a finding for deduplication across overlapping tools.
func (f Finding) key() string {
	return fmt.Sprintf("%s|%s|%s|%d", f.Tool, f.RuleID, f.File, f.Line)
}

// DedupeFindings removes exact duplicates (same tool, rule, file, line),
// preserving first-seen order. It does not merge findings across tools — a
// gosec G101 and a semgrep hit on the same line are kept separately so the
// operator sees corroborating evidence.
func DedupeFindings(in []Finding) []Finding {
	seen := make(map[string]struct{}, len(in))
	out := make([]Finding, 0, len(in))
	for _, f := range in {
		k := f.key()
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, f)
	}
	return out
}
