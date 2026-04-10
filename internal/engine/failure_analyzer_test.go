package engine

import "testing"

func TestAnalyzeFailure_UndefinedSymbol(t *testing.T) {
	hint := AnalyzeFailure("./store/store.go:42: undefined: NewStore", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
	if hint == "./store/store.go:42: undefined: NewStore" {
		t.Error("expected a helpful hint, not raw output")
	}
}

func TestAnalyzeFailure_MissingPackage(t *testing.T) {
	hint := AnalyzeFailure("cannot find package \"github.com/foo/bar\"", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_TestFailure(t *testing.T) {
	hint := AnalyzeFailure("--- FAIL: TestStore_Get (0.00s)\n    store_test.go:15: expected \"hello\", got \"\"", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_NilPointer(t *testing.T) {
	hint := AnalyzeFailure("runtime error: nil pointer dereference", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_DataRace(t *testing.T) {
	hint := AnalyzeFailure("WARNING: DATA RACE", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_ReviewFeedback(t *testing.T) {
	hint := AnalyzeFailure("", "Missing error handling in Get function")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestAnalyzeFailure_UnknownError(t *testing.T) {
	raw := "some weird error nobody anticipated"
	hint := AnalyzeFailure(raw, "")
	if hint != raw {
		t.Errorf("expected raw output as fallback, got %q", hint)
	}
}

func TestAnalyzeFailure_EmptyInputs(t *testing.T) {
	hint := AnalyzeFailure("", "")
	if hint != "" {
		t.Errorf("expected empty hint for empty inputs, got %q", hint)
	}
}

func TestAnalyzeFailure_UnusedImport(t *testing.T) {
	hint := AnalyzeFailure("\"fmt\" imported and not used", "")
	if hint == "" || hint == "\"fmt\" imported and not used" {
		t.Error("expected a helpful hint for unused import")
	}
}

func TestAnalyzeFailure_SyntaxError(t *testing.T) {
	hint := AnalyzeFailure("syntax error: unexpected newline", "")
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
}
