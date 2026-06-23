package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// PGConn wraps a single pgx connection for the SQL ops the Docker provider
// performs against its host container's admin DB. Keeps callers ignorant
// of the pgx package and lets us swap drivers later if needed.
type PGConn struct {
	conn *pgx.Conn
}

// ConnectPG opens a single connection to the named DSN. Connection failures
// are wrapped with devdb.ErrProviderDown so callers can errors.Is.
func ConnectPG(ctx context.Context, dsn string) (*PGConn, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx connect: %v: %w", err, devdb.ErrProviderDown)
	}
	return &PGConn{conn: conn}, nil
}

// Close releases the underlying pgx connection.
func (p *PGConn) Close(ctx context.Context) error {
	if p == nil || p.conn == nil {
		return nil
	}
	return p.conn.Close(ctx)
}

// CreateDB runs CREATE DATABASE "<name>". Caller is responsible for naming validation.
func (p *PGConn) CreateDB(ctx context.Context, name string) error {
	_, err := p.conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name))
	return err
}

// CreateDBFromTemplate runs CREATE DATABASE "<name>" WITH TEMPLATE "<template>".
// Caller must ensure no active connections to template (use SetTemplateFlag first).
func (p *PGConn) CreateDBFromTemplate(ctx context.Context, name, template string) error {
	_, err := p.conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q WITH TEMPLATE %q`, name, template))
	return err
}

// DropDB runs DROP DATABASE IF EXISTS "<name>".
// Existing connections are NOT terminated here; call KillConnections first.
func (p *PGConn) DropDB(ctx context.Context, name string) error {
	_, err := p.conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name))
	return err
}

// KillConnections terminates all backends for the named DB so DropDB can succeed.
func (p *PGConn) KillConnections(ctx context.Context, name string) error {
	_, err := p.conn.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`,
		name,
	)
	return err
}

// SetTemplateFlag marks a DB as a template (datistemplate=true). Postgres rejects
// new client connections to a template DB, which is what we want for fork sources.
func (p *PGConn) SetTemplateFlag(ctx context.Context, name string, on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	_, err := p.conn.Exec(ctx,
		fmt.Sprintf(`UPDATE pg_database SET datistemplate = %s WHERE datname = $1`, val),
		name,
	)
	return err
}

// ListDBsWithPrefix returns DB names starting with prefix (excluding templates).
func (p *PGConn) ListDBsWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	rows, err := p.conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1 AND NOT datistemplate ORDER BY datname`,
		prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// ListTemplates returns datname for all rows where datistemplate=true (excluding
// the built-in template0/template1).
func (p *PGConn) ListTemplates(ctx context.Context) ([]string, error) {
	rows, err := p.conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datistemplate AND datname NOT IN ('template0','template1') ORDER BY datname`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// DumpSchema returns a deterministic text representation of the connected DB's
// schema (tables, columns, primary keys). Used by the docker provider via
// Provider.Schema().
func DumpSchema(ctx context.Context, pg *PGConn) (string, error) {
	rows, err := pg.conn.Query(ctx, `
		SELECT table_schema, table_name, column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema NOT IN ('pg_catalog','information_schema')
		ORDER BY table_schema, table_name, ordinal_position
	`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out strings.Builder
	curr := ""
	for rows.Next() {
		var schema, table, col, dtype, nullable string
		if err := rows.Scan(&schema, &table, &col, &dtype, &nullable); err != nil {
			return "", err
		}
		key := schema + "." + table
		if key != curr {
			out.WriteString("\nTABLE " + key + "\n")
			curr = key
		}
		fmt.Fprintf(&out, "  %s %s (nullable=%s)\n", col, dtype, nullable)
	}
	return out.String(), rows.Err()
}
