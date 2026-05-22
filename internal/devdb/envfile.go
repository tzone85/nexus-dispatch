package devdb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileDirName is the directory inside a worktree where devdb writes its files.
const EnvFileDirName = ".nxd-db"

// WriteEnvFiles renders .nxd-db/{connect.env, README.md, psql.sh} into worktreeDir.
// File permissions: 0600 for connect.env, 0644 for README.md, 0755 for psql.sh.
func WriteEnvFiles(worktreeDir string, db DB) error {
	dir := filepath.Join(worktreeDir, EnvFileDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("devdb: mkdir %s: %w", dir, err)
	}

	envLines := []string{
		"DATABASE_URL=" + db.ConnectionString,
	}
	if db.ReadOnlyDSN != "" {
		envLines = append(envLines, "DATABASE_URL_READONLY="+db.ReadOnlyDSN)
	}
	envLines = append(envLines,
		"DATABASE_PROVIDER="+db.Provider,
		"DATABASE_ID="+db.ID,
		"DATABASE_NAME="+db.Name,
	)
	envBody := strings.Join(envLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "connect.env"), []byte(envBody), 0o600); err != nil {
		return fmt.Errorf("devdb: write connect.env: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(buildReadme(db)), 0o644); err != nil {
		return fmt.Errorf("devdb: write README.md: %w", err)
	}

	const psql = `#!/usr/bin/env bash
set -eu
# shellcheck disable=SC1091
source "$(dirname "$0")/connect.env"
exec psql "$DATABASE_URL" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "psql.sh"), []byte(psql), 0o755); err != nil {
		return fmt.Errorf("devdb: write psql.sh: %w", err)
	}
	return nil
}

// WriteFallbackNotice writes a README.md only, explaining the provider is down
// and the agent should not assume a DB is available.
func WriteFallbackNotice(worktreeDir string, cause error) error {
	dir := filepath.Join(worktreeDir, EnvFileDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("devdb: mkdir %s: %w", dir, err)
	}
	body := fmt.Sprintf(`# Ephemeral database — UNAVAILABLE (fallback)

The devdb provider was unavailable when this story started:

    %v

There is no database to connect to. Proceed without DB access for this story.
If you need a DB, escalate to the operator.
`, cause)
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(body), 0o644)
}

func buildReadme(db DB) string {
	return fmt.Sprintf(`# Your ephemeral database

You have a real Postgres database to yourself. It dies when this story finishes.

- Connection string: see connect.env
- Quick connect: ./psql.sh
- Provider: %s
- Name: %s

You can:
- Run migrations
- Insert / update / delete data
- Drop tables, create extensions
- Anything — blast radius is this DB only

You cannot:
- Touch production (this is not production)
- Assume the DB persists past this story
`, db.Provider, db.Name)
}
