package engine

import "strings"

// AnalyzeFailure examines QA output and review feedback to produce a targeted
// fix hint. Returns the raw output if no pattern matches.
func AnalyzeFailure(qaOutput, reviewFeedback string) string {
	combined := qaOutput + " " + reviewFeedback
	lower := strings.ToLower(combined)

	patterns := []struct {
		match string
		hint  string
	}{
		{"undefined:", "Build error: undefined symbol. Check that the function/type is exported (capitalized) and properly imported."},
		{"cannot find package", "Missing dependency. Run 'go mod tidy' or add the correct import path."},
		{"imported and not used", "Unused import. Remove the import or use the package."},
		{"declared and not used", "Unused variable. Remove it or use it."},
		{"cannot use", "Type mismatch. Check the function signature and ensure argument types match."},
		{"nil pointer dereference", "Nil pointer. Add a nil check before dereferencing the pointer."},
		{"race condition", "Data race detected. Add sync.Mutex or use channels for shared state."},
		{"data race", "Data race detected. Add sync.Mutex or use channels for shared state."},
		{"connection refused", "Service not running. Check that the required service (database, API) is started."},
		{"permission denied", "Permission error. Check file permissions and user access."},
		{"no such file or directory", "File not found. Check the path exists and is spelled correctly."},
		{"syntax error", "Syntax error. Check for missing brackets, semicolons, or typos."},
		{"timeout", "Operation timed out. Increase timeout or check for deadlocks."},
		{"--- fail:", "Test failure. Read the test output carefully and fix the failing assertion."},
		{"missing error handling", "Add error handling: check returned errors and handle them appropriately."},
		{"missing test", "Add unit tests for the new code."},
	}

	for _, p := range patterns {
		if strings.Contains(lower, p.match) {
			return p.hint
		}
	}

	if qaOutput != "" {
		return qaOutput
	}
	return reviewFeedback
}
