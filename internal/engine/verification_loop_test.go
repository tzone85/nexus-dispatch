package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestParseGoTestJSON_CountsTestCompileFailure locks in the fix for the
// completion-gate false-green: when a package's _test.go files fail to
// compile, `go test -json` reports the failure only as package-scoped events
// ("build-fail" and a "fail" carrying FailedBuild) that have NO Test field.
// The old parser skipped every event with an empty Test field, so it returned
// 0 failing and the gate reported green on a suite that does not compile.
func TestParseGoTestJSON_CountsTestCompileFailure(t *testing.T) {
	// Verbatim shape emitted by `go test -count=1 -json ./...` for a package
	// whose test file references an undefined symbol.
	output := `{"ImportPath":"vtest [vtest.test]","Action":"build-output","Output":"# vtest [vtest.test]\n"}
{"ImportPath":"vtest [vtest.test]","Action":"build-output","Output":"./lib_test.go:5:5: undefined: AddOldName\n"}
{"ImportPath":"vtest [vtest.test]","Action":"build-fail"}
{"Time":"2026-07-20T05:17:41Z","Action":"start","Package":"vtest"}
{"Time":"2026-07-20T05:17:41Z","Action":"output","Package":"vtest","Output":"FAIL\tvtest [build failed]\n"}
{"Time":"2026-07-20T05:17:41Z","Action":"fail","Package":"vtest","Elapsed":0,"FailedBuild":"vtest [vtest.test]"}`

	passing, failing, _ := parseGoTestJSON(output)
	if passing != 0 {
		t.Errorf("expected 0 passing on a compile failure, got %d", passing)
	}
	if failing == 0 {
		t.Fatal("BUG: a test-compile failure was parsed as 0 failing — the completion gate would report green on non-compiling tests")
	}
}

// TestParseGoTestJSON_NormalResultsUnaffected guards against over-counting: a
// clean run and a genuine test failure must still parse to the expected
// counts. A package-level "fail" WITHOUT FailedBuild (a real assertion
// failure) must not be counted twice.
func TestParseGoTestJSON_NormalResultsUnaffected(t *testing.T) {
	pass := `{"Action":"run","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"pass","Package":"p"}`
	if p, f, _ := parseGoTestJSON(pass); p != 1 || f != 0 {
		t.Errorf("clean run: want 1 pass / 0 fail, got %d/%d", p, f)
	}

	// One failing test → exactly one failure (the per-test "fail"); the
	// package-level "fail" without FailedBuild must not add a second.
	fail := `{"Action":"run","Test":"TestA"}
{"Action":"fail","Test":"TestA"}
{"Action":"fail","Package":"p"}`
	if p, f, _ := parseGoTestJSON(fail); p != 0 || f != 1 {
		t.Errorf("failing run: want 0 pass / 1 fail, got %d/%d", p, f)
	}
}

// TestRunVerificationLoop_NonCompilingTests_NotGreen is the end-to-end
// regression: a module whose non-test code builds cleanly but whose _test.go
// does not compile must NOT be reported as verifiable-green. `go build ./...`
// passes (it skips _test.go), so before the fix TestsFailing was 0 and
// ShouldRunFixCycle returned false — the gate would emit REQ_COMPLETED.
func TestRunVerificationLoop_NonCompilingTests_NotGreen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real go toolchain verification in -short mode")
	}
	repoDir := t.TempDir()
	mustWrite(t, filepath.Join(repoDir, "go.mod"), "module vcheck\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(repoDir, "lib.go"), "package vcheck\n\nfunc Add(a, b int) int { return a + b }\n")
	// Non-test code builds; the test references an undefined symbol.
	mustWrite(t, filepath.Join(repoDir, "lib_test.go"),
		"package vcheck\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif AddOldName(1, 2) != 3 {\n\t\tt.Fail()\n\t}\n}\n")

	result := RunVerificationLoop(context.Background(), repoDir, 1)

	if !result.BuildPasses {
		t.Fatalf("precondition: `go build ./...` should pass (it skips _test.go); got BuildPasses=false")
	}
	if result.TestsFailing == 0 {
		t.Fatal("BUG: non-compiling tests reported 0 failing — completion gate would false-green")
	}
	if !ShouldRunFixCycle(result) {
		t.Error("ShouldRunFixCycle should be true when the composed mainline's tests do not compile")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
