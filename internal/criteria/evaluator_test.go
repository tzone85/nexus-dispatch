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

func TestTestPasses_RealProject(t *testing.T) {
	// Create a minimal Go project with a passing test.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package testproject\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "add_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal(\"wrong\") }\n}\n"), 0o644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeTestPasses, Target: "./..."})
	if !r.Passed {
		t.Errorf("expected tests to pass, got: %s", r.Message)
	}
}

func TestTestPasses_FailingProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "fail.go"), []byte("package testproject\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "fail_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestAlwaysFails(t *testing.T) { t.Fatal(\"intentional fail\") }\n"), 0o644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeTestPasses, Target: "./..."})
	if r.Passed {
		t.Error("expected tests to fail")
	}
}

func TestTestPasses_DefaultTarget(t *testing.T) {
	// When Target is empty, it should default to "./...".
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ok.go"), []byte("package testproject\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ok_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n"), 0o644)

	r := Evaluate(context.Background(), dir, Criterion{Type: TypeTestPasses})
	if !r.Passed {
		t.Errorf("expected pass with default target, got: %s", r.Message)
	}
}

func TestCoverageAbove_Pass(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package testproject\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "add_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 { t.Fatal(\"wrong\") }\n}\n"), 0o644)

	// This simple project should have 100% coverage.
	r := Evaluate(context.Background(), dir, Criterion{Type: TypeCoverageAbove, Expected: "50.0"})
	if !r.Passed {
		t.Errorf("expected coverage above 50%%, got: %s (actual: %s)", r.Message, r.Actual)
	}
}

func TestCoverageAbove_Fail(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "funcs.go"), []byte("package testproject\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "funcs_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { A() }\n"), 0o644)

	// Only ~33% coverage — threshold of 99% should fail.
	r := Evaluate(context.Background(), dir, Criterion{Type: TypeCoverageAbove, Expected: "99.0"})
	if r.Passed {
		t.Errorf("expected coverage below 99%%, got: %s", r.Actual)
	}
}

func TestCoverageAbove_InvalidThreshold(t *testing.T) {
	r := Evaluate(context.Background(), t.TempDir(), Criterion{Type: TypeCoverageAbove, Expected: "not-a-number"})
	if r.Passed {
		t.Error("expected fail for invalid threshold")
	}
}

func TestUnknownCriterionType(t *testing.T) {
	r := Evaluate(context.Background(), t.TempDir(), Criterion{Type: "nonexistent_type"})
	if r.Passed {
		t.Error("expected fail for unknown criterion type")
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
