package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeArgvCaptureBridge writes a stub Python bridge that records its argv
// to a file and returns the given JSON. Letting tests assert on the
// captured args closes the gap that hid the wakeup/--max-results
// regressions: the previous fixed-response stub passed even when the Go
// caller sent argument names the real bridge does not accept.
func writeArgvCaptureBridge(t *testing.T, tmp, response string) (scriptPath, argvFile string) {
	t.Helper()
	argvFile = filepath.Join(tmp, "argv.txt")
	scriptPath = filepath.Join(tmp, "mempalace_bridge.py")
	content := fmt.Sprintf(`import sys
with open(%q, "w") as f:
    f.write("\n".join(sys.argv[1:]))
print(%q)
`, argvFile, response)
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write capture bridge: %v", err)
	}
	return scriptPath, argvFile
}

func readArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// TestSearch_SendsResultsFlag is the regression for the silent --max-results
// vs --results mismatch found in 2026-05. The bridge accepts --results;
// any other flag name causes argparse to reject the call and the Go side
// silently swallows the error, returning empty results forever.
func TestSearch_SendsResultsFlag(t *testing.T) {
	tmp := t.TempDir()
	bridge, argvFile := writeArgvCaptureBridge(t, tmp, `{"status":"ok","results":[]}`)
	mp := &MemPalace{bridgePath: bridge, available: true}

	if _, err := mp.Search("hello", "w", "r", 7); err != nil {
		t.Fatalf("Search: %v", err)
	}

	argv := readArgv(t, argvFile)
	if !contains(argv, "--results") {
		t.Errorf("expected argv to contain --results; got %v", argv)
	}
	if contains(argv, "--max-results") {
		t.Errorf("argv must not contain --max-results (real bridge rejects it); got %v", argv)
	}
	// Verify the value followed the flag.
	for i, a := range argv {
		if a == "--results" && (i+1 >= len(argv) || argv[i+1] != "7") {
			t.Errorf("expected --results 7, got argv %v", argv)
		}
	}
}

// TestWakeUp_SendsHyphenatedSubcommand is the regression for the silent
// "wakeup" vs "wake-up" mismatch. The real bridge defines wake-up as the
// subcommand; sending "wakeup" returns argparse error and the Go side
// silently degrades to empty result.
func TestWakeUp_SendsHyphenatedSubcommand(t *testing.T) {
	tmp := t.TempDir()
	bridge, argvFile := writeArgvCaptureBridge(t, tmp, `{"status":"ok","message":"hi"}`)
	mp := &MemPalace{bridgePath: bridge, available: true}

	if _, err := mp.WakeUp("wing-x"); err != nil {
		t.Fatalf("WakeUp: %v", err)
	}

	argv := readArgv(t, argvFile)
	if !contains(argv, "wake-up") {
		t.Errorf("expected argv to contain wake-up; got %v", argv)
	}
	if contains(argv, "wakeup") {
		t.Errorf("argv must not contain bare 'wakeup' (real bridge rejects it); got %v", argv)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
