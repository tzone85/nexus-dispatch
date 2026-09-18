package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTailLog_NonPositiveLinesShowsAllNoPanic proves tailLog does not panic on a
// non-positive --lines value. Before the fix, `--lines=-1` computed
// lines[len(lines)-(-1):] and panicked with slice-out-of-range; n <= 0 now
// means "show all".
func TestTailLog_NonPositiveLinesShowsAllNoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(path, []byte("line-a\nline-b\nline-c\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{-1, 0} {
		var buf bytes.Buffer
		if err := tailLog(path, n, true, &buf); err != nil {
			t.Fatalf("tailLog(n=%d) returned error: %v", n, err)
		}
		out := buf.String()
		for _, want := range []string{"line-a", "line-b", "line-c"} {
			if !strings.Contains(out, want) {
				t.Errorf("tailLog(n=%d) should show all lines, missing %q in %q", n, want, out)
			}
		}
	}
}

// TestTailLog_PositiveLinesTails confirms the normal tail behavior is unchanged.
func TestTailLog_PositiveLinesTails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(path, []byte("l1\nl2\nl3\nl4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tailLog(path, 2, true, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "l2") || !strings.Contains(out, "l3") || !strings.Contains(out, "l4") {
		t.Errorf("tailLog(n=2) should show only the last 2 lines, got %q", out)
	}
}
