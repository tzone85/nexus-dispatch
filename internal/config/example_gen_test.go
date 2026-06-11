package config_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

var updateExample = flag.Bool("update-example", false,
	"regenerate nxd.config.example.yaml at the repo root from DefaultYAML()")

// TestExampleYAMLMatchesDefault is a drift detector. The example config that
// ships in the repo root MUST match `config.DefaultYAML()` byte-for-byte so
// users copying it never end up with a config that disagrees with NXD's
// validator or with the file `nxd init` writes. Run with
//
//	go test ./internal/config -update-example -run TestExampleYAMLMatchesDefault
//
// to regenerate the file after intentional default changes.
func TestExampleYAMLMatchesDefault(t *testing.T) {
	want, err := config.DefaultYAML()
	if err != nil {
		t.Fatalf("DefaultYAML: %v", err)
	}
	path := filepath.Join("..", "..", "nxd.config.example.yaml")
	if *updateExample {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("write example: %v", err)
		}
		t.Logf("regenerated %s (%d bytes)", path, len(want))
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading example yaml at %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("nxd.config.example.yaml has drifted from DefaultYAML().\n"+
			"Regenerate with:\n"+
			"  go test ./internal/config -update-example -run TestExampleYAMLMatchesDefault\n"+
			"got %d bytes, want %d bytes", len(got), len(want))
	}
}
