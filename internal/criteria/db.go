package criteria

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tzone85/nexus-dispatch/internal/shellexec"
)

// readDatabaseURL returns the DATABASE_URL value from .nxd-db/connect.env in workDir,
// or empty string if the file is missing or the var isn't present.
func readDatabaseURL(workDir string) string {
	f, err := os.Open(filepath.Join(workDir, ".nxd-db", "connect.env"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "DATABASE_URL=") {
			return strings.TrimPrefix(line, "DATABASE_URL=")
		}
	}
	return ""
}

// evaluateMigrationSucceeds runs the configured command in workDir with
// DATABASE_URL set from .nxd-db/connect.env. Passes if exit code is zero.
func evaluateMigrationSucceeds(ctx context.Context, workDir string, c Criterion) Result {
	if c.Command == "" {
		return Result{Criterion: c, Passed: false,
			Message: "migration_succeeds requires `command` field"}
	}
	// The command runs through a shell (shellexec → sh -c / cmd.exe /C), so
	// shell metacharacters would allow command chaining, redirection, or
	// exfiltration. Criteria can originate from LLM "split" actions, not just
	// the operator's nxd.yaml, so reject anything that could break out of a
	// single migration-tool invocation. Migration tools (migrate, goose,
	// alembic, prisma, psql ...) never legitimately need these characters.
	if strings.ContainsAny(c.Command, ";&|`$<>\n") {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("migration command rejected: shell metacharacters not allowed in %q", c.Command)}
	}
	dsn := readDatabaseURL(workDir)
	if dsn == "" {
		return Result{Criterion: c, Passed: false,
			Message: "no .nxd-db/connect.env in worktree — devdb not provisioned for this story"}
	}
	// shellexec.CommandContext picks the right shell per OS (sh on Unix,
	// cmd.exe on Windows; NXD_SHELL overrides). Direct exec.Command("sh", ...)
	// would silently fail on native Windows even for read-only criteria
	// evaluation against an externally-provisioned DB.
	cmd := shellexec.CommandContext(ctx, c.Command)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("migration command failed: %v: %s", err, strings.TrimSpace(string(out)))}
	}
	return Result{Criterion: c, Passed: true,
		Actual:  strings.TrimSpace(string(out)),
		Message: "migration command succeeded"}
}

// evaluateSchemaChanged dumps the current schema and compares against either
// the baseline file path (if SchemaBaseline is set) or .nxd-db/baseline-schema.txt
// in the worktree. Passes if the dump differs from the baseline (non-empty diff).
func evaluateSchemaChanged(ctx context.Context, workDir string, c Criterion) Result {
	dsn := readDatabaseURL(workDir)
	if dsn == "" {
		return Result{Criterion: c, Passed: false,
			Message: "no .nxd-db/connect.env in worktree — devdb not provisioned for this story"}
	}
	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(connCtx, dsn)
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("schema_changed: pgx connect failed: %v", err)}
	}
	defer func() { _ = conn.Close(connCtx) }()

	current, err := dumpSchemaText(connCtx, conn)
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("schema_changed: dump failed: %v", err)}
	}

	baselinePath := c.SchemaBaseline
	if baselinePath == "" {
		baselinePath = filepath.Join(workDir, ".nxd-db", "baseline-schema.txt")
	} else {
		// schema_changed previously honoured absolute SchemaBaseline values
		// and "../" traversal. That lets a malicious nxd.yaml read any host
		// file as a QA check (the dump-vs-baseline comparison reveals diff
		// content in the failure message). Force baseline into the worktree.
		safe, sErr := resolveWorkDirPath(workDir, baselinePath)
		if sErr != nil {
			return Result{Criterion: c, Passed: false,
				Message: fmt.Sprintf("schema_changed: rejected baseline %q: %v", baselinePath, sErr)}
		}
		baselinePath = safe
	}
	baseline, err := os.ReadFile(baselinePath)
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("schema_changed: cannot read baseline %s: %v", baselinePath, err)}
	}
	if string(baseline) == current {
		return Result{Criterion: c, Passed: false,
			Message: "schema_changed: no diff between current and baseline schema"}
	}
	return Result{Criterion: c, Passed: true,
		Actual:  "schema differs from baseline (as expected)",
		Message: "schema_changed: schema differs from baseline"}
}

// validateReadOnlyQuery accepts only a single SELECT or WITH statement.
// It rejects embedded statement separators (so stacked statements like
// "SELECT 1; DROP TABLE x" cannot run) and any non-read leading keyword.
func validateReadOnlyQuery(sql string) error {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return fmt.Errorf("empty sql")
	}
	// Allow a single optional trailing semicolon; reject any interior one,
	// which would indicate stacked statements.
	body := strings.TrimRight(trimmed, "; \t\r\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("multiple statements not allowed")
	}
	upper := strings.ToUpper(body)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("only SELECT/WITH queries allowed")
	}
	return nil
}

// evaluateSQLQueryReturns runs the configured SQL against the story DB.
// Passes if the query returns at least one row, OR exactly ExpectedRows rows when set.
func evaluateSQLQueryReturns(ctx context.Context, workDir string, c Criterion) Result {
	if c.SQL == "" {
		return Result{Criterion: c, Passed: false,
			Message: "sql_query_returns requires `sql` field"}
	}
	// sql_query_returns is a read-only assertion: it counts rows. Restrict it
	// to a single SELECT/WITH statement so a hostile criterion (e.g. from an
	// LLM split action) can't run DROP/DELETE/UPDATE or stack a second
	// statement against the story's real Postgres instance.
	if err := validateReadOnlyQuery(c.SQL); err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("sql_query_returns: %v", err)}
	}
	dsn := readDatabaseURL(workDir)
	if dsn == "" {
		return Result{Criterion: c, Passed: false,
			Message: "no .nxd-db/connect.env in worktree — devdb not provisioned for this story"}
	}
	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(connCtx, dsn)
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("sql_query_returns: pgx connect failed: %v", err)}
	}
	defer func() { _ = conn.Close(connCtx) }()
	rows, err := conn.Query(connCtx, c.SQL)
	if err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("sql_query_returns: query failed: %v", err)}
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return Result{Criterion: c, Passed: false,
			Message: fmt.Sprintf("sql_query_returns: rows error: %v", err)}
	}
	if c.ExpectedRows != nil {
		if count != *c.ExpectedRows {
			return Result{Criterion: c, Passed: false,
				Actual:  fmt.Sprintf("%d rows", count),
				Message: fmt.Sprintf("sql_query_returns: got %d rows, want %d", count, *c.ExpectedRows)}
		}
		return Result{Criterion: c, Passed: true,
			Actual:  fmt.Sprintf("%d rows", count),
			Message: fmt.Sprintf("sql_query_returns: matched %d rows", count)}
	}
	if count == 0 {
		return Result{Criterion: c, Passed: false,
			Message: "sql_query_returns: query returned zero rows"}
	}
	return Result{Criterion: c, Passed: true,
		Actual:  fmt.Sprintf("%d rows", count),
		Message: fmt.Sprintf("sql_query_returns: returned %d rows", count)}
}

// dumpSchemaText returns a deterministic text representation of the connected
// DB's schema. Mirrors DumpSchema in internal/devdb but uses pgx directly to
// avoid an import cycle.
func dumpSchemaText(ctx context.Context, conn *pgx.Conn) (string, error) {
	rows, err := conn.Query(ctx, `
		SELECT table_schema, table_name, column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema NOT IN ('pg_catalog','information_schema')
		ORDER BY table_schema, table_name, ordinal_position
	`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	curr := ""
	for rows.Next() {
		var schema, table, col, dtype, nullable string
		if err := rows.Scan(&schema, &table, &col, &dtype, &nullable); err != nil {
			return "", err
		}
		key := schema + "." + table
		if key != curr {
			b.WriteString("\nTABLE " + key + "\n")
			curr = key
		}
		fmt.Fprintf(&b, "  %s %s (nullable=%s)\n", col, dtype, nullable)
	}
	return b.String(), rows.Err()
}
