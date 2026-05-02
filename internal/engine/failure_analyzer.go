package engine

import (
	"strings"
	"sync"
)

// RecoveryStrategy classifies a build/test/review failure and returns a
// targeted fix hint. Strategies are evaluated in registration order; the
// first match wins, so register more-specific patterns before generic
// ones. The combined argument is the lowercased concatenation of the QA
// output and the reviewer feedback.
type RecoveryStrategy struct {
	// Name is a stable identifier (lowercase, no spaces) used in tests
	// and event payloads so a recovery action can be attributed.
	Name string
	// Match is called once with the lower-cased combined input. Return
	// true to claim the failure.
	Match func(lower string) bool
	// Hint is the human-readable instruction handed back to the agent.
	Hint string
}

// recoveryRegistry holds the active set of strategies. Default strategies
// are seeded by init(). RegisterRecoveryStrategy / ResetRecoveryStrategies
// let test code or plugins extend / replace the list.
var (
	recoveryMu       sync.RWMutex
	recoveryRegistry []RecoveryStrategy
)

// containsAny is a tiny helper for substring-set match strategies.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// substringStrategy is a convenience constructor for simple "match if any
// of these substrings appears" strategies. Inputs are expected to be
// lowercase already (matching the AnalyzeFailure contract).
func substringStrategy(name, hint string, needles ...string) RecoveryStrategy {
	return RecoveryStrategy{
		Name:  name,
		Match: func(lower string) bool { return containsAny(lower, needles...) },
		Hint:  hint,
	}
}

// defaultRecoveryStrategies returns a fresh slice of the built-in
// strategies. Returned slice is owned by the caller so tests can mutate
// it without polluting the global registry.
func defaultRecoveryStrategies() []RecoveryStrategy {
	return []RecoveryStrategy{
		substringStrategy("undefined_symbol",
			"Build error: undefined symbol. Check that the function/type is exported (capitalized) and properly imported.",
			"undefined:"),
		substringStrategy("missing_package",
			"Missing dependency. Run 'go mod tidy' or add the correct import path.",
			"cannot find package"),
		substringStrategy("unused_import",
			"Unused import. Remove the import or use the package.",
			"imported and not used"),
		substringStrategy("unused_var",
			"Unused variable. Remove it or use it.",
			"declared and not used"),
		substringStrategy("type_mismatch",
			"Type mismatch. Check the function signature and ensure argument types match.",
			"cannot use"),
		substringStrategy("nil_pointer",
			"Nil pointer. Add a nil check before dereferencing the pointer.",
			"nil pointer dereference"),
		substringStrategy("data_race",
			"Data race detected. Add sync.Mutex or use channels for shared state.",
			"race condition", "data race"),
		substringStrategy("conn_refused",
			"Service not running. Check that the required service (database, API) is started.",
			"connection refused"),
		substringStrategy("perm_denied",
			"Permission error. Check file permissions and user access.",
			"permission denied"),
		substringStrategy("file_not_found",
			"File not found. Check the path exists and is spelled correctly.",
			"no such file or directory"),
		substringStrategy("syntax_error",
			"Syntax error. Check for missing brackets, semicolons, or typos.",
			"syntax error"),
		substringStrategy("timeout",
			"Operation timed out. Increase timeout or check for deadlocks.",
			"timeout"),
		substringStrategy("test_failure",
			"Test failure. Read the test output carefully and fix the failing assertion.",
			"--- fail:"),
		substringStrategy("missing_error_handling",
			"Add error handling: check returned errors and handle them appropriately.",
			"missing error handling"),
		substringStrategy("missing_tests",
			"Add unit tests for the new code.",
			"missing test"),
	}
}

func init() {
	recoveryRegistry = defaultRecoveryStrategies()
}

// RegisterRecoveryStrategy appends a new strategy to the recovery
// registry. The added strategy runs LAST, after all built-ins, so this is
// safe for adding fall-through patterns. To override a built-in, call
// ResetRecoveryStrategies first and re-register the desired set.
func RegisterRecoveryStrategy(s RecoveryStrategy) {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	recoveryRegistry = append(recoveryRegistry, s)
}

// ResetRecoveryStrategies replaces the active registry. Pass nil to
// restore the built-in defaults. Useful for tests that want to verify a
// specific strategy in isolation.
func ResetRecoveryStrategies(strategies []RecoveryStrategy) {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	if strategies == nil {
		recoveryRegistry = defaultRecoveryStrategies()
		return
	}
	recoveryRegistry = append([]RecoveryStrategy(nil), strategies...)
}

// RecoveryStrategies returns a snapshot of the active registry.
func RecoveryStrategies() []RecoveryStrategy {
	recoveryMu.RLock()
	defer recoveryMu.RUnlock()
	return append([]RecoveryStrategy(nil), recoveryRegistry...)
}

// AnalyzeFailure examines QA output and review feedback to produce a
// targeted fix hint. Returns the raw output if no pattern matches.
//
// The implementation walks the recovery-strategy registry; the first
// strategy whose Match returns true wins. Behaviour is unchanged from the
// previous switch-based implementation — the registry just makes the
// strategies addable / replaceable from test code or plugins.
func AnalyzeFailure(qaOutput, reviewFeedback string) string {
	combined := qaOutput + " " + reviewFeedback
	lower := strings.ToLower(combined)

	recoveryMu.RLock()
	strategies := recoveryRegistry
	recoveryMu.RUnlock()

	for _, s := range strategies {
		if s.Match != nil && s.Match(lower) {
			return s.Hint
		}
	}

	if qaOutput != "" {
		return qaOutput
	}
	return reviewFeedback
}
