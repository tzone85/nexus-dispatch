package criteria

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupNXDDB creates a temp worktree with a populated .nxd-db/connect.env.
func setupNXDDB(t *testing.T, dsn string) string {
	t.Helper()
	workDir := t.TempDir()
	dir := filepath.Join(workDir, ".nxd-db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connect.env"),
		[]byte("DATABASE_URL="+dsn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workDir
}

// --- migration_succeeds ---

func TestEvaluate_MigrationSucceeds_NoDevDB(t *testing.T) {
	workDir := t.TempDir() // no .nxd-db
	c := Criterion{
		Type:    TypeMigrationSucceeds,
		Command: "echo ok",
	}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected failure when .nxd-db is absent, got: %+v", result)
	}
	if !strings.Contains(result.Message, "devdb not provisioned") {
		t.Errorf("expected helpful failure message, got: %s", result.Message)
	}
}

func TestEvaluate_MigrationSucceeds_MissingCommand(t *testing.T) {
	workDir := setupNXDDB(t, "postgres://x@x/x")
	c := Criterion{Type: TypeMigrationSucceeds}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected failure when command empty")
	}
	if !strings.Contains(result.Message, "requires `command`") {
		t.Errorf("expected helpful failure message, got: %s", result.Message)
	}
}

func TestEvaluate_MigrationSucceeds_CommandRuns(t *testing.T) {
	// Use a harmless command that doesn't need a real DB.
	workDir := setupNXDDB(t, "postgres://x@x/x")
	c := Criterion{
		Type:    TypeMigrationSucceeds,
		Command: "true", // shell builtin, always exits 0
	}
	result := Evaluate(context.Background(), workDir, c)
	if !result.Passed {
		t.Errorf("expected pass for `true`, got: %s", result.Message)
	}
}

func TestEvaluate_MigrationSucceeds_CommandFails(t *testing.T) {
	workDir := setupNXDDB(t, "postgres://x@x/x")
	c := Criterion{
		Type:    TypeMigrationSucceeds,
		Command: "false",
	}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected fail for `false`, got pass")
	}
	if !strings.Contains(result.Message, "migration command failed") {
		t.Errorf("expected 'migration command failed' in message, got: %s", result.Message)
	}
}

// --- sql_query_returns ---

func TestEvaluate_SQLQueryReturns_NoDevDB(t *testing.T) {
	workDir := t.TempDir()
	c := Criterion{
		Type: TypeSQLQueryReturns,
		SQL:  "SELECT 1",
	}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected failure when no .nxd-db, got: %+v", result)
	}
	if !strings.Contains(result.Message, "devdb not provisioned") {
		t.Errorf("expected 'devdb not provisioned' in message, got: %s", result.Message)
	}
}

func TestEvaluate_SQLQueryReturns_MissingSQL(t *testing.T) {
	workDir := setupNXDDB(t, "postgres://x@x/x")
	c := Criterion{Type: TypeSQLQueryReturns}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected failure when sql empty")
	}
	if !strings.Contains(result.Message, "requires `sql`") {
		t.Errorf("expected 'requires `sql`' in message, got: %s", result.Message)
	}
}

// --- schema_changed ---

func TestEvaluate_SchemaChanged_NoDevDB(t *testing.T) {
	workDir := t.TempDir()
	c := Criterion{Type: TypeSchemaChanged}
	result := Evaluate(context.Background(), workDir, c)
	if result.Passed {
		t.Errorf("expected failure when no .nxd-db")
	}
	if !strings.Contains(result.Message, "devdb not provisioned") {
		t.Errorf("expected 'devdb not provisioned' in message, got: %s", result.Message)
	}
}

// --- readDatabaseURL ---

func TestReadDatabaseURL_ReturnsURL(t *testing.T) {
	workDir := setupNXDDB(t, "postgres://user:pass@localhost/testdb")
	got := readDatabaseURL(workDir)
	if got != "postgres://user:pass@localhost/testdb" {
		t.Errorf("readDatabaseURL = %q, want postgres://user:pass@localhost/testdb", got)
	}
}

func TestReadDatabaseURL_Missing(t *testing.T) {
	workDir := t.TempDir()
	got := readDatabaseURL(workDir)
	if got != "" {
		t.Errorf("readDatabaseURL = %q, want empty", got)
	}
}

func TestReadDatabaseURL_EmptyFile(t *testing.T) {
	workDir := t.TempDir()
	dir := filepath.Join(workDir, ".nxd-db")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "connect.env"), []byte("# no url here\n"), 0o600)
	got := readDatabaseURL(workDir)
	if got != "" {
		t.Errorf("readDatabaseURL = %q, want empty", got)
	}
}
