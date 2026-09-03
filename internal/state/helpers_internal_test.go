package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPayloadHelpers(t *testing.T) {
	m := map[string]any{
		"f": 2.5, "i": 3, "s": "str", "sub": map[string]any{"k": "v"}, "bad": []any{1},
	}
	if payloadFloat(m, "f") != 2.5 || payloadFloat(m, "i") != 3 || payloadFloat(m, "s") != 0 || payloadFloat(m, "missing") != 0 {
		t.Error("payloadFloat")
	}
	if payloadInt(m, "f") != 2 || payloadInt(m, "i") != 3 || payloadInt(m, "s") != 0 || payloadInt(m, "missing") != 0 {
		t.Error("payloadInt")
	}
	if payloadStr(m, "s") != "str" || payloadStr(m, "i") != "" || payloadStr(m, "missing") != "" {
		t.Error("payloadStr")
	}
	if payloadMap(m, "sub")["k"] != "v" || len(payloadMap(m, "bad")) != 0 || len(payloadMap(m, "missing")) != 0 {
		t.Error("payloadMap")
	}
}

func TestBase64Len(t *testing.T) {
	// "abc" → "YWJj" (4) + 2 quotes; "a" → "YQ==" (4) + 2 quotes.
	if base64Len(3) != 6 || base64Len(1) != 6 || base64Len(0) != 2 {
		t.Errorf("base64Len(3)=%d (1)=%d (0)=%d", base64Len(3), base64Len(1), base64Len(0))
	}
}

func TestCutString(t *testing.T) {
	if _, ok := cutString("short", 3); ok {
		t.Error("strings <= 16 bytes must not be cut")
	}
	s, ok := cutString(strings.Repeat("a", 100), 10)
	if !ok || !strings.HasPrefix(s, strings.Repeat("a", 90)) || !strings.HasSuffix(s, "…[truncated 10 bytes]") {
		t.Errorf("cut = %q ok=%v", s, ok)
	}
	// Cutting more than available leaves the 16-byte floor.
	s, ok = cutString(strings.Repeat("b", 100), 500)
	if !ok || !strings.HasPrefix(s, strings.Repeat("b", 16)+"…") {
		t.Errorf("floor cut = %q", s)
	}
}

func TestStripMarker(t *testing.T) {
	tests := []struct {
		in      string
		content string
		n       int
	}{
		{"plain", "plain", 0},
		{"abc…[truncated 12 bytes]", "abc", 12},
		{"abc…[truncated x bytes]", "abc…[truncated x bytes]", 0},
		{"abc…[truncated 12 bytes] tail", "abc…[truncated 12 bytes] tail", 0},
	}
	for _, tc := range tests {
		c, n := stripMarker(tc.in)
		if c != tc.content || n != tc.n {
			t.Errorf("stripMarker(%q) = %q,%d; want %q,%d", tc.in, c, n, tc.content, tc.n)
		}
	}
}

func TestRewriteWithout_MissingSource(t *testing.T) {
	if _, err := rewriteWithout(filepath.Join(t.TempDir(), "nope"), nil, false); err == nil {
		t.Fatal("expected error")
	}
}

func TestRewriteWithout_WithholdsTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":2}\n{\"torn\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := rewriteWithout(path, map[int]struct{}{1: {}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want 1", kept)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "{\"b\":2}\n" {
		t.Errorf("rewritten = %q", got)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

func TestCopyFile_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "out")); err == nil {
		t.Error("missing source should error")
	}
	src := filepath.Join(dir, "src")
	_ = os.WriteFile(src, []byte("x"), 0o600)
	if err := copyFile(src, filepath.Join(dir, "no", "such", "dir")); err == nil {
		t.Error("unwritable destination should error")
	}
}

func TestTornFragment_MissingFile(t *testing.T) {
	if _, _, err := tornFragment(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error")
	}
}

func TestTornFragment_IntactAndEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e.jsonl")
	_ = os.WriteFile(path, nil, 0o600)
	off, frag, err := tornFragment(path)
	if err != nil || off != 0 || frag != nil {
		t.Errorf("empty: off=%d frag=%q err=%v", off, frag, err)
	}
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)
	off, frag, err = tornFragment(path)
	if err != nil || off != 3 || frag != nil {
		t.Errorf("intact: off=%d frag=%q err=%v", off, frag, err)
	}
}

func TestReadLines_MissingFile(t *testing.T) {
	if _, err := readLines(filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadAllEvents_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e.jsonl")
	_ = os.WriteFile(path, []byte("{bad\n"), 0o600)
	if _, err := readAllEvents(path); err == nil {
		t.Fatal("expected error")
	}
	if _, err := readAllEvents(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("expected error")
	}
}

func TestAppendQuarantine_UnwritableDir(t *testing.T) {
	if err := appendQuarantine(filepath.Join(t.TempDir(), "no", "dir", "events.jsonl"), [][]byte{[]byte("x")}); err == nil {
		t.Fatal("expected error")
	}
}

func TestFileStore_ReopenLocked_MissingDir(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	fs.path = filepath.Join(dir, "gone", "events.jsonl")
	if err := fs.reopenLocked(); err == nil {
		t.Fatal("expected error reopening under a missing directory")
	}
}

func TestFileStore_Append_TornRepairFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	fs, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	// Point the store at a path that does not exist so the torn-tail probe fails.
	fs.path = filepath.Join(dir, "missing.jsonl")
	if err := fs.Append(NewEvent(EventStoryProgress, "a", "s", nil)); err == nil {
		t.Fatal("expected torn-tail probe error")
	}
}

func TestFileStore_Append_MarshalRoundTripsEmptyPayloadOverCap(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(filepath.Join(dir, "events.jsonl"), WithMaxEventBytes(10))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	// Envelope alone exceeds the cap and there is no payload to trim: write anyway.
	if err := fs.Append(NewEvent(EventStoryProgress, "agent", "story", nil)); err != nil {
		t.Fatal(err)
	}
	n, err := fs.Count(EventFilter{})
	if err != nil || n != 1 {
		t.Errorf("Count = %d, %v", n, err)
	}
}

func TestFileStore_List_UnreadablePath(t *testing.T) {
	dir := t.TempDir()
	fs, _ := NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer fs.Close()
	fs.path = filepath.Join(dir, "missing.jsonl")
	if _, err := fs.List(EventFilter{}); err == nil {
		t.Fatal("expected error")
	}
}
