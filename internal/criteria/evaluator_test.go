package criteria

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists_Pass(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeFileExists, Target: "main.go"})
	if !r.Passed {
		t.Errorf("expected pass, got: %s", r.Message)
	}
}

func TestFileExists_Fail(t *testing.T) {
	r := Evaluate(context.Background(), t.TempDir(), Criterion{Type: TypeFileExists, Target: "missing.go"})
	if r.Passed {
		t.Error("expected fail for missing file")
	}
}

func TestFileContains_Substring(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc hello() {}"), 0644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeFileContains, Target: "main.go", Expected: "func hello"})
	if !r.Passed {
		t.Errorf("expected pass, got: %s", r.Message)
	}
}

func TestFileContains_Regex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("func Add(a, b int) int { return a + b }"), 0644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeFileContains, Target: "main.go", Expected: `func \w+\(`})
	if !r.Passed {
		t.Errorf("expected regex match, got: %s", r.Message)
	}
}

func TestFileContains_Fail(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeFileContains, Target: "main.go", Expected: "not here"})
	if r.Passed {
		t.Error("expected fail for missing content")
	}
}

func TestCommandSucceeds_Pass(t *testing.T) {
	r := Evaluate(context.Background(), t.TempDir(), Criterion{Type: TypeCommandSucceeds, Target: "echo hello"})
	if !r.Passed {
		t.Errorf("expected pass, got: %s", r.Message)
	}
}

func TestCommandSucceeds_Fail(t *testing.T) {
	r := Evaluate(context.Background(), t.TempDir(), Criterion{Type: TypeCommandSucceeds, Target: "false"})
	if r.Passed {
		t.Error("expected fail for 'false' command")
	}
}

func TestEvaluateAll(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)

	results := EvaluateAll(context.Background(), dir, []Criterion{
		{Type: TypeFileExists, Target: "a.txt"},
		{Type: TypeFileExists, Target: "missing.txt"},
	})

	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	if !results[0].Passed {
		t.Error("results[0] should pass")
	}
	if results[1].Passed {
		t.Error("results[1] should fail")
	}
	if AllPassed(results) {
		t.Error("AllPassed should be false")
	}
}

func TestFailureSummary(t *testing.T) {
	results := []Result{
		{Criterion: Criterion{Type: TypeFileExists, Target: "a.go"}, Passed: true},
		{Criterion: Criterion{Type: TypeFileExists, Target: "b.go"}, Passed: false, Message: "not found"},
	}
	summary := FailureSummary(results)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestParseCoverage(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"ok  pkg 0.5s coverage: 85.3% of statements", 85.3},
		{"no coverage info", -1},
		{"coverage: 100.0% of statements", 100.0},
	}
	for _, tt := range tests {
		got := parseCoverage(tt.input)
		if got != tt.want {
			t.Errorf("parseCoverage(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}
