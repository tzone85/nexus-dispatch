package devdb_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

func TestWriteEnvFiles_CreatesAllThree(t *testing.T) {
	dir := t.TempDir()
	db := devdb.DB{
		ID:               "abc123",
		Name:             "nxd-myproj-story-1",
		Provider:         "docker",
		ConnectionString: "postgres://u:p@localhost:5432/nxd-myproj-story-1?sslmode=disable",
		ReadOnlyDSN:      "postgres://u:p@localhost:5432/nxd-myproj-story-1?sslmode=disable&options=-c+default_transaction_read_only=on",
	}
	if err := devdb.WriteEnvFiles(dir, db); err != nil {
		t.Fatalf("WriteEnvFiles: %v", err)
	}

	envPath := filepath.Join(dir, ".nxd-db", "connect.env")
	readmePath := filepath.Join(dir, ".nxd-db", "README.md")
	psqlPath := filepath.Join(dir, ".nxd-db", "psql.sh")

	for _, p := range []string{envPath, readmePath, psqlPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s: %v", p, err)
		}
	}

	envBytes, _ := os.ReadFile(envPath)
	env := string(envBytes)
	if !strings.Contains(env, "DATABASE_URL=postgres://") {
		t.Errorf("connect.env missing DATABASE_URL: %s", env)
	}
	if !strings.Contains(env, "DATABASE_URL_READONLY=postgres://") {
		t.Errorf("connect.env missing DATABASE_URL_READONLY: %s", env)
	}
	if !strings.Contains(env, "DATABASE_PROVIDER=docker") {
		t.Errorf("connect.env missing DATABASE_PROVIDER")
	}
}

func TestWriteEnvFiles_WritesSelfIgnoringGitignore(t *testing.T) {
	// connect.env contains the Postgres admin password in its DSN. The .nxd-db
	// directory must carry a self-ignoring .gitignore so that a `git add -A`
	// (by the agent or the pipeline's autoCommit) cannot leak it into a PR.
	dir := t.TempDir()
	db := devdb.DB{
		Name:             "nxd-myproj-story-1",
		ConnectionString: "postgres://postgres:s3cr3t-admin-pw@localhost:5432/nxd-myproj-story-1?sslmode=disable",
	}
	if err := devdb.WriteEnvFiles(dir, db); err != nil {
		t.Fatalf("WriteEnvFiles: %v", err)
	}
	giPath := filepath.Join(dir, ".nxd-db", ".gitignore")
	b, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("expected .nxd-db/.gitignore: %v", err)
	}
	if strings.TrimSpace(string(b)) != "*" {
		t.Errorf(".nxd-db/.gitignore should ignore everything (\"*\"), got %q", string(b))
	}
}

func TestWriteEnvFiles_EnvFileMode0600(t *testing.T) {
	dir := t.TempDir()
	db := devdb.DB{Name: "x", ConnectionString: "postgres://x@x/x"}
	if err := devdb.WriteEnvFiles(dir, db); err != nil {
		t.Fatalf("WriteEnvFiles: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, ".nxd-db", "connect.env"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("connect.env mode = %o, want 0600", mode)
	}
}

func TestWriteEnvFiles_PsqlIsExecutable(t *testing.T) {
	dir := t.TempDir()
	db := devdb.DB{Name: "x", ConnectionString: "postgres://x@x/x"}
	if err := devdb.WriteEnvFiles(dir, db); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".nxd-db", "psql.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("psql.sh not executable: %o", info.Mode().Perm())
	}
}

func TestWriteFallbackNotice_Writes(t *testing.T) {
	dir := t.TempDir()
	if err := devdb.WriteFallbackNotice(dir, devdb.ErrProviderDown); err != nil {
		t.Fatalf("WriteFallbackNotice: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".nxd-db", "README.md"))
	if err != nil {
		t.Fatalf("readme not written: %v", err)
	}
	if !strings.Contains(string(b), "fallback") && !strings.Contains(string(b), "unavailable") {
		t.Errorf("fallback notice should mention fallback or unavailable, got: %s", string(b))
	}
}
