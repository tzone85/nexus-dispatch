//go:build !integration

package docker

import (
	"strings"
	"testing"
)

func TestProvider_DSN_UsesConfiguredHost(t *testing.T) {
	p := NewProvider(Config{
		HostPortRange: "5500-5500",
		Host:          "192.168.64.3",
	})
	p.cfg.AdminPassword = "pw"
	p.cfg.HostPort = 5500
	dsn := p.dbDSN("mydb", false)
	if !strings.Contains(dsn, "@192.168.64.3:5500/mydb") {
		t.Errorf("DSN should use configured Host, got: %s", dsn)
	}
}

func TestProvider_DSN_DefaultsToLocalhost(t *testing.T) {
	p := NewProvider(Config{HostPortRange: "5500-5500"})
	p.cfg.AdminPassword = "pw"
	p.cfg.HostPort = 5500
	dsn := p.dbDSN("mydb", false)
	if !strings.Contains(dsn, "@localhost:5500/mydb") {
		t.Errorf("DSN should default to localhost, got: %s", dsn)
	}
}

func TestProvider_DSN_ReadOnly_UsesConfiguredHost(t *testing.T) {
	p := NewProvider(Config{
		HostPortRange: "5500-5500",
		Host:          "192.168.64.3",
	})
	p.cfg.AdminPassword = "pw"
	p.cfg.HostPort = 5500
	dsn := p.dbDSN("mydb", true)
	if !strings.Contains(dsn, "@192.168.64.3:5500/mydb") {
		t.Errorf("ReadOnly DSN should use configured Host, got: %s", dsn)
	}
	if !strings.Contains(dsn, "default_transaction_read_only") {
		t.Errorf("ReadOnly DSN should include read-only option, got: %s", dsn)
	}
}
