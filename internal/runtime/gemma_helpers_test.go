package runtime

import "testing"

// TestLooksLikeTestFile covers the test-file detection helper used
// by the gemma runtime to identify generated test files for the
// criteria-gated completion check. Was 0% pre-#33.
func TestLooksLikeTestFile(t *testing.T) {
	cases := map[string]bool{
		"main_test.go":         true,
		"foo/bar_test.go":      true,
		"component.test.ts":    true,
		"component.test.tsx":   true,
		"file.test.js":         true,
		"file.test.jsx":        true,
		"thing.spec.ts":        true,
		"thing.spec.tsx":       true,
		"thing.spec.js":        true,
		"thing.spec.jsx":       true,
		"module_test.py":       true,
		"module_spec.py":       true,
		"file_test.rb":         true,
		"file_spec.rb":         true,
		"file_test.rs":         true,
		// negatives
		"main.go":              false,
		"index.ts":             false,
		"app.py":               false,
		"README.md":            false,
		"":                     false,
		// mixed case (function lowercases the basename)
		"Main_Test.go":         true,
		"File.Test.TS":         true,
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := looksLikeTestFile(path); got != want {
				t.Errorf("looksLikeTestFile(%q) = %v, want %v", path, got, want)
			}
		})
	}
}
