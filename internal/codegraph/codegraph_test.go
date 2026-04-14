package codegraph

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// --- Analysis tests ---

func TestImpactAnalysis_Empty(t *testing.T) {
	var nilIA *ImpactAnalysis
	if !nilIA.Empty() {
		t.Error("nil ImpactAnalysis should be empty")
	}
	empty := &ImpactAnalysis{}
	if !empty.Empty() {
		t.Error("zero-value ImpactAnalysis should be empty")
	}
	nonEmpty := &ImpactAnalysis{ChangedFunctions: []ChangedNode{{Name: "foo"}}}
	if nonEmpty.Empty() {
		t.Error("ImpactAnalysis with changed functions should not be empty")
	}
}

func TestImpactAnalysis_FormatMarkdown(t *testing.T) {
	ia := &ImpactAnalysis{
		RiskScore: 0.75,
		Summary:   "3 changed functions, 1 test gap",
		ReviewPriorities: []ChangedNode{
			{Name: "DoWork", FilePath: "/repo/internal/engine/worker.go", LineStart: 10, LineEnd: 30, RiskScore: 0.8},
			{Name: "Helper", FilePath: "/repo/internal/utils/helper.go", LineStart: 5, LineEnd: 15, RiskScore: 0.4},
		},
		TestGaps: []TestGap{
			{Name: "DoWork", FilePath: "/repo/internal/engine/worker.go", LineStart: 10, LineEnd: 30},
		},
	}

	md := ia.FormatMarkdown()
	if md == "" {
		t.Fatal("expected non-empty markdown")
	}

	checks := []string{
		"risk: 0.75/1.0",
		"Review Priorities",
		"DoWork",
		"engine/worker.go:10-30",
		"Test Gaps",
		"no test coverage",
	}
	for _, want := range checks {
		if !contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestImpactAnalysis_FormatMarkdown_Empty(t *testing.T) {
	ia := &ImpactAnalysis{}
	if ia.FormatMarkdown() != "" {
		t.Error("empty analysis should produce empty markdown")
	}
}

func TestUniqueAffectedFiles(t *testing.T) {
	ia := &ImpactAnalysis{
		ReviewPriorities: []ChangedNode{
			{Name: "A", FilePath: "/repo/a.go", IsTest: false},
			{Name: "B", FilePath: "/repo/b.go", IsTest: false},
			{Name: "A2", FilePath: "/repo/a.go", IsTest: false},
			{Name: "TestA", FilePath: "/repo/a_test.go", IsTest: true},
		},
	}

	files := ia.UniqueAffectedFiles()
	if len(files) != 2 {
		t.Fatalf("expected 2 unique files, got %d: %v", len(files), files)
	}
	if files[0] != "/repo/a.go" || files[1] != "/repo/b.go" {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestShortPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"/a/b/c/d/e.go", "d/e.go"},
		{"a.go", "a.go"},
		{"dir/file.go", "dir/file.go"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		if got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Runner tests ---

func TestNewRunner_Available(t *testing.T) {
	r := NewRunner()
	// code-review-graph should be installed from task #1
	if !r.Available() {
		t.Skip("code-review-graph not installed, skipping")
	}
	if r.BinPath == "" {
		t.Error("BinPath should be set when Available")
	}
}

func TestRunner_Unavailable_GracefulDegradation(t *testing.T) {
	r := &Runner{} // no binary

	if r.Available() {
		t.Error("empty runner should not be available")
	}

	ctx := context.Background()

	ia, err := r.DetectChanges(ctx, "/tmp", "HEAD~1")
	if err != nil {
		t.Errorf("DetectChanges should not error when unavailable: %v", err)
	}
	if !ia.Empty() {
		t.Error("DetectChanges should return empty analysis when unavailable")
	}

	info, err := r.Status(ctx, "/tmp")
	if err != nil {
		t.Errorf("Status should not error when unavailable: %v", err)
	}
	if info.NodeCount != 0 {
		t.Error("Status should return zero counts when unavailable")
	}
}

func TestParseDetectChanges(t *testing.T) {
	raw := `{
		"summary": "2 changed functions, 1 test gap",
		"risk_score": 0.45,
		"changed_functions": [
			{"name": "Foo", "file_path": "/a.go", "kind": "Function", "line_start": 1, "line_end": 10, "is_test": false, "risk_score": 0.5},
			{"name": "TestBar", "file_path": "/a_test.go", "kind": "Test", "line_start": 1, "line_end": 5, "is_test": true, "risk_score": 0.1}
		],
		"test_gaps": [
			{"name": "Foo", "file": "/a.go", "line_start": 1, "line_end": 10}
		],
		"review_priorities": [
			{"name": "Foo", "file_path": "/a.go", "kind": "Function", "line_start": 1, "line_end": 10, "is_test": false, "risk_score": 0.5}
		],
		"affected_flows": []
	}`

	ia, err := parseDetectChanges(raw)
	if err != nil {
		t.Fatalf("parseDetectChanges: %v", err)
	}
	if ia.RiskScore != 0.45 {
		t.Errorf("RiskScore = %v, want 0.45", ia.RiskScore)
	}
	if len(ia.ChangedFunctions) != 2 {
		t.Errorf("ChangedFunctions count = %d, want 2", len(ia.ChangedFunctions))
	}
	if len(ia.TestGaps) != 1 {
		t.Errorf("TestGaps count = %d, want 1", len(ia.TestGaps))
	}
	if len(ia.ReviewPriorities) != 1 {
		t.Errorf("ReviewPriorities count = %d, want 1", len(ia.ReviewPriorities))
	}
	if len(ia.AffectedFiles) != 1 || ia.AffectedFiles[0] != "/a.go" {
		t.Errorf("AffectedFiles = %v, want [/a.go]", ia.AffectedFiles)
	}
}

func TestParseStatus(t *testing.T) {
	raw := `Nodes: 2189
Edges: 24062
Files: 241
Languages: go, javascript
Last updated: 2026-04-13T16:12:11
Built on branch: main
Built at commit: 5606cd45de68`

	info, err := parseStatus(raw)
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if info.NodeCount != 2189 {
		t.Errorf("NodeCount = %d, want 2189", info.NodeCount)
	}
	if info.EdgeCount != 24062 {
		t.Errorf("EdgeCount = %d, want 24062", info.EdgeCount)
	}
	if info.FileCount != 241 {
		t.Errorf("FileCount = %d, want 241", info.FileCount)
	}
	if len(info.Languages) != 2 || info.Languages[0] != "go" {
		t.Errorf("Languages = %v, want [go, javascript]", info.Languages)
	}
	if info.CommitHash != "5606cd45de68" {
		t.Errorf("CommitHash = %q, want 5606cd45de68", info.CommitHash)
	}
}

// --- GraphDB tests ---

func TestGraphDB_Stats(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	info, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if info.NodeCount != 3 {
		t.Errorf("NodeCount = %d, want 3", info.NodeCount)
	}
	if info.EdgeCount != 2 {
		t.Errorf("EdgeCount = %d, want 2", info.EdgeCount)
	}
	if info.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", info.FileCount)
	}
}

func TestGraphDB_CallersOf(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	callers, err := db.CallersOf("pkg/main.go::Helper")
	if err != nil {
		t.Fatalf("CallersOf: %v", err)
	}
	if len(callers) != 1 {
		t.Fatalf("expected 1 caller, got %d", len(callers))
	}
	if callers[0].Name != "DoWork" {
		t.Errorf("caller name = %q, want DoWork", callers[0].Name)
	}
}

func TestGraphDB_TestsFor(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tests, err := db.TestsFor("pkg/main.go")
	if err != nil {
		t.Fatalf("TestsFor: %v", err)
	}
	if len(tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(tests))
	}
	if tests[0].Name != "TestDoWork" {
		t.Errorf("test name = %q, want TestDoWork", tests[0].Name)
	}
}

func TestGraphDB_Open_NotFound(t *testing.T) {
	_, err := Open("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestGraphDBPath(t *testing.T) {
	got := GraphDBPath("/my/repo")
	want := "/my/repo/.code-review-graph/graph.db"
	if got != want {
		t.Errorf("GraphDBPath = %q, want %q", got, want)
	}
}

// --- test helpers ---

func setupTestDB(t *testing.T) *GraphDB {
	t.Helper()
	dir := t.TempDir()
	dbDir := filepath.Join(dir, GraphDir)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "graph.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
		CREATE TABLE nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			qualified_name TEXT NOT NULL UNIQUE,
			file_path TEXT NOT NULL,
			line_start INTEGER,
			line_end INTEGER,
			language TEXT,
			parent_name TEXT,
			params TEXT,
			return_type TEXT,
			modifiers TEXT,
			is_test INTEGER DEFAULT 0,
			file_hash TEXT,
			extra TEXT DEFAULT '{}',
			updated_at REAL NOT NULL,
			signature TEXT,
			community_id INTEGER
		);
		CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			source_qualified TEXT NOT NULL,
			target_qualified TEXT NOT NULL,
			file_path TEXT NOT NULL,
			line INTEGER DEFAULT 0,
			extra TEXT DEFAULT '{}',
			updated_at REAL NOT NULL
		);
		CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	// Insert test data
	now := 1713000000.0
	_, err = db.Exec(`INSERT INTO nodes (kind, name, qualified_name, file_path, line_start, line_end, language, is_test, updated_at)
		VALUES ('Function', 'DoWork', 'pkg/main.go::DoWork', 'pkg/main.go', 10, 30, 'go', 0, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO nodes (kind, name, qualified_name, file_path, line_start, line_end, language, is_test, updated_at)
		VALUES ('Function', 'Helper', 'pkg/main.go::Helper', 'pkg/main.go', 35, 45, 'go', 0, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO nodes (kind, name, qualified_name, file_path, line_start, line_end, language, is_test, updated_at)
		VALUES ('Test', 'TestDoWork', 'pkg/main_test.go::TestDoWork', 'pkg/main_test.go', 5, 20, 'go', 1, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}

	// DoWork calls Helper
	_, err = db.Exec(`INSERT INTO edges (kind, source_qualified, target_qualified, file_path, line, updated_at)
		VALUES ('CALLS', 'pkg/main.go::DoWork', 'pkg/main.go::Helper', 'pkg/main.go', 15, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	// TestDoWork calls DoWork
	_, err = db.Exec(`INSERT INTO edges (kind, source_qualified, target_qualified, file_path, line, updated_at)
		VALUES ('CALLS', 'pkg/main_test.go::TestDoWork', 'pkg/main.go::DoWork', 'pkg/main_test.go', 10, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}

	db.Close()

	gdb, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return gdb
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
