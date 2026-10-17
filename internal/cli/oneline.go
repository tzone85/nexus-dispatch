package cli

import "strings"

// oneLine folds a multi-line message onto one line. Every command that prints
// a git failure beside a story prints one line per story, and git writes
// several: a raw error would break the report's shape (assertReportLines).
func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "; ")
}
