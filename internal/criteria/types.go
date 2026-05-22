package criteria

// Type identifies the kind of declarative success criterion.
type Type string

const (
	TypeFileExists      Type = "file_exists"
	TypeFileContains    Type = "file_contains"
	TypeTestPasses      Type = "test_passes"
	TypeCoverageAbove   Type = "coverage_above"
	TypeCommandSucceeds Type = "command_succeeds"

	// SP5 — DB-touching criteria.
	TypeMigrationSucceeds Type = "migration_succeeds"
	TypeSchemaChanged     Type = "schema_changed"
	TypeSQLQueryReturns   Type = "sql_query_returns"
)

// Criterion defines a single declarative success check.
type Criterion struct {
	Type     Type   `json:"type"`
	Target   string `json:"target"`   // file path, test package, or command
	Expected string `json:"expected"` // substring, regex, threshold, etc.

	// SP5 additions — DB-touching criteria.
	Command        string `yaml:"command,omitempty" json:"command,omitempty"`                  // migration_succeeds: shell command to run
	SQL            string `yaml:"sql,omitempty" json:"sql,omitempty"`                          // sql_query_returns: query to execute
	ExpectedRows   *int   `yaml:"expected_rows,omitempty" json:"expected_rows,omitempty"`       // sql_query_returns: optional exact row count
	SchemaBaseline string `yaml:"schema_baseline,omitempty" json:"schema_baseline,omitempty"` // schema_changed: path to baseline file
}

// Result holds the outcome of evaluating a single criterion.
type Result struct {
	Criterion Criterion `json:"criterion"`
	Passed    bool      `json:"passed"`
	Actual    string    `json:"actual"`
	Message   string    `json:"message"`
}
