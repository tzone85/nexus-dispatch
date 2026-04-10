package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SearchResult represents a single result returned by a MemPalace search.
type SearchResult struct {
	Text       string  `json:"text"`
	Wing       string  `json:"wing"`
	Room       string  `json:"room"`
	Similarity float64 `json:"similarity"`
}

// MemPalace wraps the Python mempalace_bridge.py script, providing semantic
// memory operations to the NXD engine. All methods degrade gracefully: when
// the bridge or Python is not available, they return zero-value results
// without errors so NXD can operate without MemPalace installed.
type MemPalace struct {
	bridgePath string
	palacePath string
	available  bool
}

// bridgeOutput is the envelope returned by mempalace_bridge.py.
type bridgeOutput struct {
	Status  string         `json:"status"`
	Results []SearchResult `json:"results,omitempty"`
	Message string         `json:"message,omitempty"`
}

// NewMemPalace auto-detects the bridge script path relative to the working
// directory and the running executable, then performs a health check.
func NewMemPalace() *MemPalace {
	bridge := detectBridgePath()
	mp := &MemPalace{
		bridgePath: bridge,
	}
	mp.available = mp.healthCheck()
	return mp
}

// NewMemPalaceWithPath creates a MemPalace with explicit bridge and palace
// paths. Useful for testing and custom deployments.
func NewMemPalaceWithPath(bridgePath, palacePath string) *MemPalace {
	mp := &MemPalace{
		bridgePath: bridgePath,
		palacePath: palacePath,
	}
	mp.available = mp.healthCheck()
	return mp
}

// IsAvailable reports whether the MemPalace bridge is reachable and healthy.
func (mp *MemPalace) IsAvailable() bool {
	return mp.available
}

// Search queries the palace for semantically similar entries. When the bridge
// is unavailable the method returns an empty slice and nil error.
func (mp *MemPalace) Search(query, wing, room string, maxResults int) ([]SearchResult, error) {
	if !mp.available {
		return nil, nil
	}

	args := []string{"search", "--query", query}
	if wing != "" {
		args = append(args, "--wing", wing)
	}
	if room != "" {
		args = append(args, "--room", room)
	}
	if maxResults > 0 {
		args = append(args, "--max-results", fmt.Sprintf("%d", maxResults))
	}

	out, err := mp.runBridge(args...)
	if err != nil {
		return nil, nil
	}

	return parseSearchOutput(out), nil
}

// Mine stores a text fragment in the specified wing and room.
func (mp *MemPalace) Mine(wing, room, text string) error {
	if !mp.available {
		return nil
	}
	args := []string{"mine", "--wing", wing, "--room", room, "--text", text}
	_, err := mp.runBridge(args...)
	if err != nil {
		return nil
	}
	return nil
}

// MineMeta is a convenience wrapper that mines into the nxd_meta wing.
func (mp *MemPalace) MineMeta(text string) error {
	return mp.Mine("nxd_meta", "meta", text)
}

// WakeUp retrieves a summary for the given wing. Returns an empty string when
// the bridge is unavailable.
func (mp *MemPalace) WakeUp(wing string) (string, error) {
	if !mp.available {
		return "", nil
	}
	args := []string{"wakeup", "--wing", wing}
	out, err := mp.runBridge(args...)
	if err != nil {
		return "", nil
	}

	var bo bridgeOutput
	if err := json.Unmarshal([]byte(out), &bo); err != nil {
		return "", nil
	}
	if bo.Status != "ok" {
		return "", nil
	}
	return bo.Message, nil
}

// runBridge invokes the Python bridge with the supplied arguments and returns
// its stdout. The --palace flag is prepended when a custom palace path is set.
func (mp *MemPalace) runBridge(args ...string) (string, error) {
	if mp.bridgePath == "" {
		return "", fmt.Errorf("bridge path not set")
	}

	cmdArgs := []string{mp.bridgePath}
	if mp.palacePath != "" {
		cmdArgs = append(cmdArgs, "--palace", mp.palacePath)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("python3", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bridge command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// healthCheck runs the bridge health command and returns true when the bridge
// reports status "ok".
func (mp *MemPalace) healthCheck() bool {
	out, err := mp.runBridge("health")
	if err != nil {
		return false
	}

	var bo bridgeOutput
	if err := json.Unmarshal([]byte(out), &bo); err != nil {
		return false
	}
	return bo.Status == "ok"
}

// detectBridgePath looks for scripts/mempalace_bridge.py relative to the
// working directory and the running executable's directory.
func detectBridgePath() string {
	candidates := buildCandidatePaths()
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// buildCandidatePaths returns the list of locations to probe for the bridge
// script, in priority order.
func buildCandidatePaths() []string {
	const rel = "scripts/mempalace_bridge.py"

	var paths []string

	// Relative to working directory.
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, rel))
	}

	// Relative to the running executable.
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), rel))
	}

	// Relative to this source file (useful during go test).
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		// internal/memory/mempalace.go -> repo root is two dirs up.
		root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
		paths = append(paths, filepath.Join(root, rel))
	}

	return paths
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// parseSearchOutput extracts search results from the bridge JSON output.
// Returns an empty slice on any parse error or non-ok status.
func parseSearchOutput(raw string) []SearchResult {
	if raw == "" {
		return nil
	}

	var bo bridgeOutput
	if err := json.Unmarshal([]byte(raw), &bo); err != nil {
		return nil
	}
	if bo.Status != "ok" {
		return nil
	}

	// Return a new slice (never mutate the decoded one).
	results := make([]SearchResult, len(bo.Results))
	copy(results, bo.Results)
	return results
}
