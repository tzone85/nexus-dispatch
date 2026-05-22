package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// MaxTemplateDumpSize caps how much SQL we will read from a template dump.
// 256 MiB is large enough for real prod snapshots while preventing a runaway
// reader from filling memory. Adjust if your dumps exceed this.
const MaxTemplateDumpSize = 256 << 20 // 256 MiB

// TempTemplateName returns "<name>-tmp-<random4hex>" used as the staging
// name during atomic RefreshTemplate.
func TempTemplateName(name string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return name + "-tmp-" + hex.EncodeToString(b[:])
}

// CreateTemplate creates a new template DB by:
//  1. CREATE DATABASE name
//  2. connect to it as admin
//  3. read SQL from dumpSQL (size-capped) and execute it
//  4. SetTemplateFlag(name, true)
//
// dumpSQL must yield SQL text understood by Postgres (plain pg_dump --format=p,
// or hand-written DDL). Binary pg_restore format is not supported in this
// version.
func (p *Provider) CreateTemplate(ctx context.Context, name string, dumpSQL io.Reader) error {
	if !devdb.IsValid(name) {
		return fmt.Errorf("%w: %q", devdb.ErrInvalidName, name)
	}
	adminPG, err := p.adminConn(ctx)
	if err != nil {
		return err
	}
	defer adminPG.Close(ctx)

	if err := adminPG.CreateDB(ctx, name); err != nil {
		return fmt.Errorf("template create %s: %w", name, err)
	}

	// Connect to the new DB and execute the dump.
	dbDSN := p.dbDSN(name, false)
	dbConn, err := ConnectPG(ctx, dbDSN)
	if err != nil {
		// Roll back the created (now-orphan) DB.
		_ = adminPG.DropDB(ctx, name)
		return fmt.Errorf("template connect %s: %w", name, err)
	}
	defer dbConn.Close(ctx)

	sql, err := io.ReadAll(io.LimitReader(dumpSQL, MaxTemplateDumpSize+1))
	if err != nil {
		_ = adminPG.DropDB(ctx, name)
		return fmt.Errorf("template read dump: %w", err)
	}
	if len(sql) > MaxTemplateDumpSize {
		_ = adminPG.DropDB(ctx, name)
		return fmt.Errorf("template dump exceeds %d bytes", MaxTemplateDumpSize)
	}

	// Execute the dump SQL. We use the pgx connection's Exec to send the
	// whole text — Postgres handles multi-statement scripts via the simple
	// query protocol.
	if _, err := dbConn.conn.Exec(ctx, string(sql)); err != nil {
		_ = adminPG.DropDB(ctx, name)
		return fmt.Errorf("template apply SQL: %w", err)
	}

	if err := adminPG.SetTemplateFlag(ctx, name, true); err != nil {
		return fmt.Errorf("template flag %s: %w", name, err)
	}
	return nil
}

// RefreshTemplate atomically replaces an existing template: it imports the new
// dump into a temp name, clears the template flag on the old, drops the old,
// then renames temp → name and re-marks the template flag.
func (p *Provider) RefreshTemplate(ctx context.Context, name string, dumpSQL io.Reader) error {
	tmp := TempTemplateName(name)
	if err := p.CreateTemplate(ctx, tmp, dumpSQL); err != nil {
		return err
	}

	pg, err := p.adminConn(ctx)
	if err != nil {
		return err
	}
	defer pg.Close(ctx)

	// Drop the old. Lift datistemplate first so DROP can proceed; ignore
	// errors if the old didn't exist.
	_ = pg.SetTemplateFlag(ctx, name, false)
	if err := pg.DropDB(ctx, name); err != nil {
		return fmt.Errorf("drop old template %s: %w", name, err)
	}

	if _, err := pg.conn.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %q RENAME TO %q`, tmp, name)); err != nil {
		return fmt.Errorf("rename tmp template %s -> %s: %w", tmp, name, err)
	}
	return pg.SetTemplateFlag(ctx, name, true)
}

// ListTemplates returns datname for all rows where datistemplate=true (except
// the built-in template0/template1). Delegates to the pg helper.
func (p *Provider) ListTemplates(ctx context.Context) ([]string, error) {
	pg, err := p.adminConn(ctx)
	if err != nil {
		return nil, err
	}
	defer pg.Close(ctx)
	return pg.ListTemplates(ctx)
}
