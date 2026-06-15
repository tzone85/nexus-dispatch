package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// F9: every Cobra command registered on the rootCmd must have a heading
// in docs/reference/cli-reference.md. Without this guard, new commands
// (like the recently-added pause/archive/models/etc.) silently drift
// out of the operator-facing reference and surprise users who go
// looking for documentation.
//
// Subcommands of `nxd config` / `nxd db` / `nxd spec` / `nxd models` are
// documented inline under their parent's section, so we only assert on
// top-level command names.
func TestDocs_CLIReferenceCoversAllCommands(t *testing.T) {
	// Locate the repo root from this test file's path so the test runs
	// the same whether invoked from the package dir or repo root.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	docPath := filepath.Join(root, "docs", "reference", "cli-reference.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(raw)

	var missing []string
	for _, c := range rootCmd.Commands() {
		name := c.Name()
		if c.Hidden || name == "help" || name == "completion" {
			continue
		}
		heading := "### nxd " + name
		if !strings.Contains(doc, heading) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("cli-reference.md is missing sections for: %v", missing)
	}
}
