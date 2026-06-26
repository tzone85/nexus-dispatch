// Package security implements nxd's security review agent: a growable
// vulnerability knowledge base, a multi-tool scanner runner, and the findings
// model shared by the standalone scan command and the per-story pipeline gate.
package security

import "strings"

// Severity ranks a finding by how urgently it must be addressed. Higher values
// are more severe so they order naturally and compare with AtLeast.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// String returns the canonical lowercase label.
func (s Severity) String() string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	case SeverityLow:
		return "low"
	default:
		return "info"
	}
}

// AtLeast reports whether s is at least as severe as min (inclusive).
func (s Severity) AtLeast(min Severity) bool { return s >= min }

// ParseSeverity maps a scanner's severity label (case-insensitive) to a
// Severity, absorbing the common synonyms different tools emit. Unknown labels
// default to Info so an unrecognised value never silently inflates risk.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "crit", "blocker":
		return SeverityCritical
	case "high", "error", "severe":
		return SeverityHigh
	case "medium", "moderate", "warning", "warn":
		return SeverityMedium
	case "low", "minor", "note":
		return SeverityLow
	default:
		return SeverityInfo
	}
}
