package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// projectKind identifies the build system used by a directory.
type projectKind int

const (
	projectUnknown projectKind = iota
	projectGo
	projectNode
	projectRust
)

// integrationBuildTimeout is the maximum time allowed for a post-merge build.
const integrationBuildTimeout = 60 * time.Second

// detectProjectKind inspects repoDir for well-known build system markers and
// returns the matching kind. Returns projectUnknown when no recognisable
// marker is found (caller treats it as a no-op).
func detectProjectKind(repoDir string) projectKind {
	switch {
	case buildFileExists(filepath.Join(repoDir, "go.mod")):
		return projectGo
	case buildFileExists(filepath.Join(repoDir, "Cargo.toml")):
		return projectRust
	case buildFileExists(filepath.Join(repoDir, "package.json")):
		return projectNode
	default:
		return projectUnknown
	}
}

// buildFileExists reports whether the given path exists on disk.
func buildFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runIntegrationBuild runs the project's build command against repoDir and
// returns combined stderr+stdout on failure, or nil on success.
//
// Detection rules (first match wins):
//   - go.mod        → go build ./...
//   - Cargo.toml    → cargo build
//   - package.json  → npm run build  (only if a "build" script exists)
//
// When no build system is recognised the function returns nil (best-effort).
func runIntegrationBuild(repoDir string) error {
	kind := detectProjectKind(repoDir)

	ctx, cancel := context.WithTimeout(context.Background(), integrationBuildTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch kind {
	case projectGo:
		cmd = exec.CommandContext(ctx, "go", "build", "./...")
	case projectRust:
		cmd = exec.CommandContext(ctx, "cargo", "build")
	case projectNode:
		if !hasNPMBuildScript(repoDir) {
			return nil // no "build" script — skip
		}
		cmd = exec.CommandContext(ctx, "npm", "run", "build")
	default:
		return nil // unrecognised build system — best-effort no-op
	}

	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := bytes.TrimSpace(out)
		if len(output) == 0 {
			return fmt.Errorf("build command exited with %v (no output)", err)
		}
		return fmt.Errorf("%s", output)
	}
	return nil
}

// hasNPMBuildScript reads package.json and checks for a "build" script entry
// without importing a full JSON parser — a simple substring check suffices.
func hasNPMBuildScript(repoDir string) bool {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return false
	}
	// Fast substring check: "build": "<something>"
	return strings.Contains(string(data), `"build"`)
}
