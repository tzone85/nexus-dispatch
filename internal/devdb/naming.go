package devdb

import (
	"regexp"
	"strings"
)

// Maximum Postgres identifier length.
const maxNameLen = 63

// PrefixNXD is the canonical naming prefix for NXD-managed databases.
const PrefixNXD = "nxd"

// Matches a valid Postgres-friendly DB name produced by FormatDBName:
// lowercase letter start, then lowercase alphanumerics or hyphens,
// up to 63 chars total.
var validRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// FormatDBName produces "<prefix>-<project>-<storyID>".
// Project is lowercased, non-alphanumerics replaced with "-", consecutive
// hyphens trimmed; the project segment is truncated so the total length
// stays within Postgres' 63-char identifier limit.
// storyID is used as-is (NXD's existing format already matches our charset:
// 8 hex chars of reqID + "-" + 2-char LLM ID, e.g. "a8cbef1f-3a").
func FormatDBName(prefix, project, storyID string) string {
	cleanProject := sanitizeSegment(project)
	cleanStory := strings.ToLower(storyID)

	// Budget for project = total - (prefix + "-" + "-" + storyID).
	fixedLen := len(prefix) + 1 + 1 + len(cleanStory)
	if budget := maxNameLen - fixedLen; len(cleanProject) > budget {
		if budget < 0 {
			budget = 0
		}
		cleanProject = cleanProject[:budget]
		cleanProject = strings.TrimRight(cleanProject, "-")
	}

	parts := []string{prefix}
	if cleanProject != "" {
		parts = append(parts, cleanProject)
	}
	parts = append(parts, cleanStory)
	return strings.Join(parts, "-")
}

// IsValid reports whether name is a Postgres-friendly identifier matching
// FormatDBName's output rules.
func IsValid(name string) bool {
	return validRe.MatchString(name)
}

// ParseStoryID extracts the storyID portion of a name produced by FormatDBName.
// Returns "" if name does not start with "<prefix>-" or the structure does
// not match the expected trailing "<8hex>-<2alphanum>" shape.
func ParseStoryID(prefix, name string) string {
	head := prefix + "-"
	if !strings.HasPrefix(name, head) {
		return ""
	}
	rest := name[len(head):]
	segs := strings.Split(rest, "-")
	if len(segs) < 2 {
		return ""
	}
	return segs[len(segs)-2] + "-" + segs[len(segs)-1]
}

var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeSegment(s string) string {
	s = strings.ToLower(s)
	s = nonAlnumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
