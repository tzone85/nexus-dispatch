//go:build !integration

package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// --- dbFromName ---

func TestDBFromName_PopulatesAllFields(t *testing.T) {
	p := NewProvider(Config{HostPortRange: "5500-5500"})
	p.cfg.AdminPassword = "secret"
	p.cfg.HostPort = 5500
	p.cfg.AdminUser = "postgres"
	p.cfg.Host = "localhost"

	db := p.dbFromName(devdb.CreateOpts{Name: "nxd-abc", Labels: map[string]string{"story_id": "S1"}})

	if db.ID != "nxd-abc" || db.Name != "nxd-abc" {
		t.Errorf("ID/Name mismatch: id=%q name=%q", db.ID, db.Name)
	}
	if db.Provider != "docker" {
		t.Errorf("Provider = %q, want docker", db.Provider)
	}
	if !strings.Contains(db.ConnectionString, "/nxd-abc") {
		t.Errorf("ConnectionString missing db name: %q", db.ConnectionString)
	}
	if db.ReadOnlyDSN != "" {
		t.Error("expected empty ReadOnlyDSN when not read-only")
	}
	if db.Labels["story_id"] != "S1" {
		t.Error("Labels not propagated")
	}
}

func TestDBFromName_ReadOnlyAddsDSN(t *testing.T) {
	p := NewProvider(Config{HostPortRange: "5500-5500"})
	p.cfg.AdminPassword = "pw"
	p.cfg.HostPort = 5500

	db := p.dbFromName(devdb.CreateOpts{Name: "nxd-ro", ReadOnly: true})

	if db.ReadOnlyDSN == "" {
		t.Fatal("expected non-empty ReadOnlyDSN when ReadOnly=true")
	}
	if !strings.Contains(db.ReadOnlyDSN, "default_transaction_read_only") {
		t.Errorf("ReadOnlyDSN missing read-only option: %q", db.ReadOnlyDSN)
	}
}

// --- applyDefaults ---

func TestApplyDefaults_Empty(t *testing.T) {
	c := applyDefaults(Config{})
	if c.Image != "postgres:16" {
		t.Errorf("Image default = %q, want postgres:16", c.Image)
	}
	if c.ContainerName != "nxd-devdb-pg16" {
		t.Errorf("ContainerName default = %q", c.ContainerName)
	}
	if c.Network != "nxd-devdb" {
		t.Errorf("Network default = %q", c.Network)
	}
	if c.HostPortRange != "5500-5599" {
		t.Errorf("HostPortRange default = %q", c.HostPortRange)
	}
	if c.AdminUser != "postgres" {
		t.Errorf("AdminUser default = %q", c.AdminUser)
	}
	if c.Host != "localhost" {
		t.Errorf("Host default = %q", c.Host)
	}
	if c.TemplateVolume == "" {
		t.Error("TemplateVolume default should not be empty")
	}
}

func TestApplyDefaults_PreservesExplicit(t *testing.T) {
	in := Config{
		Image:          "postgres:17",
		ContainerName:  "custom-pg",
		Network:        "my-net",
		HostPortRange:  "6000-6100",
		AdminUser:      "admin",
		TemplateVolume: "/tmp/foo",
		Host:           "192.168.1.10",
	}
	out := applyDefaults(in)
	if out.Image != "postgres:17" {
		t.Errorf("Image overwritten: %q", out.Image)
	}
	if out.ContainerName != "custom-pg" {
		t.Errorf("ContainerName overwritten: %q", out.ContainerName)
	}
	if out.Host != "192.168.1.10" {
		t.Errorf("Host overwritten: %q", out.Host)
	}
}

// --- loadOrCreateAdminPassword ---

func TestLoadOrCreateAdminPassword_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	storageDir := filepath.Join(dir, "storage")

	p := &Provider{}
	pw, err := p.loadOrCreateAdminPassword(storageDir)
	if err != nil {
		t.Fatalf("loadOrCreateAdminPassword: %v", err)
	}
	if len(pw) != 32 {
		t.Errorf("expected 32-char hex password, got %d chars: %q", len(pw), pw)
	}

	// Verify file written.
	contents, err := os.ReadFile(filepath.Join(storageDir, "devdb-admin.pw"))
	if err != nil {
		t.Fatalf("password file not written: %v", err)
	}
	if string(contents) != pw {
		t.Error("file contents != returned password")
	}
}

func TestLoadOrCreateAdminPassword_ReadsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := "PRESEEDED_PASSWORD"
	if err := os.WriteFile(filepath.Join(dir, "devdb-admin.pw"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Provider{}
	pw, err := p.loadOrCreateAdminPassword(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAdminPassword: %v", err)
	}
	if pw != existing {
		t.Errorf("got %q, want %q", pw, existing)
	}
}

func TestLoadOrCreateAdminPassword_FilePermissionsTight(t *testing.T) {
	dir := t.TempDir()
	storageDir := filepath.Join(dir, "storage")

	p := &Provider{}
	if _, err := p.loadOrCreateAdminPassword(storageDir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(storageDir, "devdb-admin.pw"))
	if err != nil {
		t.Fatal(err)
	}
	// File should be mode 0600 — only owner read+write.
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("password file perm = %o, want 0600 (sensitive credential)", fi.Mode().Perm())
	}

	dirFi, err := os.Stat(storageDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirFi.Mode().Perm() != 0o700 {
		t.Errorf("storage dir perm = %o, want 0700", dirFi.Mode().Perm())
	}
}

// --- Create/Fork guard on invalid name (no network needed because validation
// is first) ---

func TestCreate_InvalidNameReturnsErrBeforeNetwork(t *testing.T) {
	p := NewProvider(Config{HostPortRange: "5500-5500"})
	_, err := p.Create(context.Background(), devdb.CreateOpts{Name: "BAD CAPS WITH SPACE"})
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !errors.Is(err, devdb.ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got: %v", err)
	}
}

func TestFork_InvalidNameReturnsErrBeforeNetwork(t *testing.T) {
	p := NewProvider(Config{HostPortRange: "5500-5500"})
	_, err := p.Fork(context.Background(), "template", devdb.CreateOpts{Name: "BAD CAPS"})
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !errors.Is(err, devdb.ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got: %v", err)
	}
}
