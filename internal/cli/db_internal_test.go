package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// --- findDBByNameOrID ---

func TestFindDBByNameOrID_MatchByName(t *testing.T) {
	dbs := []devdb.DB{
		{ID: "id-1", Name: "foo"},
		{ID: "id-2", Name: "bar"},
	}
	got, err := findDBByNameOrID(dbs, "bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "id-2" {
		t.Errorf("got ID=%q, want id-2", got.ID)
	}
}

func TestFindDBByNameOrID_MatchByID(t *testing.T) {
	dbs := []devdb.DB{
		{ID: "id-1", Name: "foo"},
		{ID: "id-2", Name: "bar"},
	}
	got, err := findDBByNameOrID(dbs, "id-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "foo" {
		t.Errorf("got Name=%q, want foo", got.Name)
	}
}

func TestFindDBByNameOrID_NotFound(t *testing.T) {
	dbs := []devdb.DB{{ID: "id-1", Name: "foo"}}
	_, err := findDBByNameOrID(dbs, "missing")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), `"missing" not found`) {
		t.Errorf("expected helpful error, got: %v", err)
	}
}

func TestFindDBByNameOrID_Empty(t *testing.T) {
	_, err := findDBByNameOrID(nil, "x")
	if err == nil {
		t.Fatal("expected not-found error on empty list")
	}
}

// --- writeConfigWithDevDB writes a yaml with the given devdb.provider value
// and returns the path. Used by guard tests below.
func writeConfigWithDevDB(t *testing.T, provider string) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nxd")
	os.MkdirAll(stateDir, 0o755)

	body := strings.Builder{}
	body.WriteString("version: \"1.0\"\n")
	body.WriteString("workspace:\n  state_dir: " + stateDir + "\n  backend: sqlite\n")
	body.WriteString("merge:\n  base_branch: main\n  mode: local\n")
	body.WriteString("cleanup:\n  branch_retention_days: 7\n")
	if provider != "" {
		body.WriteString("devdb:\n  provider: " + provider + "\n")
	}

	cfgPath := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(cfgPath, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// --- dbProviderFor guards ---

func TestDBProviderFor_NullProvider_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "null")

	cmd := newDBListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)

	_, err := dbProviderFor(cmd)
	if err == nil {
		t.Fatal("expected error when devdb.provider == null")
	}
	if !strings.Contains(err.Error(), "devdb is not configured") {
		t.Errorf("expected 'devdb is not configured', got: %v", err)
	}
}

func TestDBProviderFor_UnsetProvider_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "")

	cmd := newDBListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)

	_, err := dbProviderFor(cmd)
	if err == nil {
		t.Fatal("expected error when devdb section absent")
	}
}

func TestDBProviderFor_BadConfigPath(t *testing.T) {
	cmd := newDBListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", "/does/not/exist/nxd.yaml")

	_, err := dbProviderFor(cmd)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

// --- dockerProviderFor guards ---

func TestDockerProviderFor_NonDocker_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "null")

	cmd := newDBTemplateListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)

	_, err := dockerProviderFor(cmd)
	if err == nil {
		t.Fatal("expected error for non-docker provider")
	}
	if !strings.Contains(err.Error(), "template ops require") {
		t.Errorf("expected 'template ops require' in error, got: %v", err)
	}
}

func TestDockerProviderFor_BadConfig(t *testing.T) {
	cmd := newDBTemplateListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", "/nope/nxd.yaml")

	_, err := dockerProviderFor(cmd)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

// --- delete --confirm guard exercised end-to-end ---

func TestDBDeleteCmd_WithoutConfirm_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "docker")

	cmd := newDBDeleteCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)
	cmd.SetContext(context.Background())

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"some-db"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected destructive-op error without --confirm")
	}
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("expected --confirm mention in error, got: %v", err)
	}
}

// --- list/ping/template list against null provider ---

func TestDBListCmd_NullProvider_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "null")

	cmd := newDBListCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error from null-provider db list")
	}
}

func TestDBPingCmd_NullProvider_Errors(t *testing.T) {
	cfg := writeConfigWithDevDB(t, "null")

	cmd := newDBPingCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", cfg)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected ping to fail on null provider")
	}
}
