package criteria

// Type identifies the kind of declarative success criterion.
type Type string

const (
	TypeFileExists      Type = "file_exists"
	TypeFileContains    Type = "file_contains"
	TypeTestPasses      Type = "test_passes"
	TypeCoverageAbove   Type = "coverage_above"
	TypeCommandSucceeds Type = "command_succeeds"
)

// Criterion defines a single declarative success check.
type Criterion struct {
	Type     Type   `json:"type"`
	Target   string `json:"target"`   // file path, test package, or command
	Expected string `json:"expected"` // substring, regex, threshold, etc.
}

// Result holds the outcome of evaluating a single criterion.
type Result struct {
	Criterion Criterion `json:"criterion"`
	Passed    bool      `json:"passed"`
	Actual    string    `json:"actual"`
	Message   string    `json:"message"`
}
