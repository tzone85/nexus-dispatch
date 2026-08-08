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

// TestParseGoTestJSON_BuildFailCountsAsFailing pins the regression where a test
// suite that does not COMPILE was reported as 0 failing. `go build ./...` does
// not compile _test.go files, so a cross-story break in a test's dependency
// keeps checkBuild green while `go test -json` emits a build-fail event with no
// Test field. If that event is not counted, the completion gate emits
// REQ_COMPLETED on a mainline whose tests do not build.
func TestParseGoTestJSON_BuildFailCountsAsFailing(t *testing.T) {
	// Verbatim `go test -count=1 -json ./...` output for a package whose test
	// file references an undefined symbol (captured from the real toolchain).
	out := `{"ImportPath":"demo [demo.test]","Action":"build-output","Output":"# demo [demo.test]\n"}
{"ImportPath":"demo [demo.test]","Action":"build-output","Output":"./foo_test.go:3:32: undefined: Bar\n"}
{"ImportPath":"demo [demo.test]","Action":"build-fail"}
{"Time":"2026-07-06T05:10:31.594769294Z","Action":"start","Package":"demo"}
{"Time":"2026-07-06T05:10:31.594812561Z","Action":"output","Package":"demo","Output":"FAIL\tdemo [build failed]\n"}
{"Time":"2026-07-06T05:10:31.594817791Z","Action":"fail","Package":"demo","Elapsed":0,"FailedBuild":"demo [demo.test]"}
`
	passing, failing, total := parseGoTestJSON(out)
	if failing < 1 {
		t.Fatalf("build-fail must count as a test failure: got passing=%d failing=%d total=%d", passing, failing, total)
	}
	if passing != 0 {
		t.Errorf("no tests ran, expected 0 passing, got %d", passing)
	}

	// The gate must not permit completion on this result.
	if !ShouldRunFixCycle(VerificationResult{BuildPasses: true, TestsPassing: passing, TestsFailing: failing, TestsTotal: total}) {
		t.Error("ShouldRunFixCycle must return true when the test suite fails to build")
	}
}

// TestParseGoTestJSON_CountsTestLevelOnly guards the existing behavior: only
// per-test pass/fail events are counted, so a package-level pass/fail (Test=="")
// that always accompanies test events does not double-count.
func TestParseGoTestJSON_CountsTestLevelOnly(t *testing.T) {
	out := `{"Action":"run","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"run","Test":"TestB"}
{"Action":"fail","Test":"TestB"}
{"Action":"fail","Package":"demo"}
{"Action":"output","Package":"demo","Output":"FAIL\n"}
`
	passing, failing, total := parseGoTestJSON(out)
	if passing != 1 || failing != 1 || total != 2 {
		t.Fatalf("expected 1 passing / 1 failing / 2 total, got %d/%d/%d", passing, failing, total)
	}
}

// TestParseGoTestJSON_AllGreen confirms a fully passing suite still reads clean.
func TestParseGoTestJSON_AllGreen(t *testing.T) {
	out := `{"Action":"pass","Test":"TestA"}
{"Action":"pass","Test":"TestB"}
{"Action":"pass","Package":"demo"}
`
	passing, failing, _ := parseGoTestJSON(out)
	if passing != 2 || failing != 0 {
		t.Fatalf("expected 2 passing / 0 failing, got %d/%d", passing, failing)
	}
	if ShouldRunFixCycle(VerificationResult{BuildPasses: true, TestsPassing: passing, TestsFailing: failing}) {
		t.Error("ShouldRunFixCycle must return false for a green suite")
	}
}

// TestParseGoTestJSON_CountsPerTestResults is the baseline: ordinary per-test
// pass/fail events are counted, and package-summary events (empty Test) are not
// double-counted.
func TestParseGoTestJSON_CountsPerTestResults(t *testing.T) {
	out := `{"Action":"pass","Package":"example.com/a","Test":"TestFoo"}
{"Action":"fail","Package":"example.com/a","Test":"TestBar"}
{"Action":"pass","Package":"example.com/a","Test":"TestBaz"}
{"Action":"fail","Package":"example.com/a"}` // package summary — must not add a 2nd failure
	passing, failing, total := parseGoTestJSON(out)
	if passing != 2 || failing != 1 || total != 3 {
		t.Fatalf("got passing=%d failing=%d total=%d, want 2/1/3", passing, failing, total)
	}
}

// TestParseGoTestJSON_TestCompileFailureCountsAsFailing is the regression guard
// for the completion-gate false negative: a package whose *tests* do not
// compile emits package-level "build-fail"/"fail" events with an empty Test
// field and no per-test events. These were previously dropped, yielding
// 0 passing / 0 failing, so the gate certified a red mainline as green. They
// must now be counted as failures.
func TestParseGoTestJSON_TestCompileFailureCountsAsFailing(t *testing.T) {
	// Shape emitted by `go test -json ./...` when a _test.go fails to compile.
	out := `{"Action":"start","Package":"example.com/x"}
{"Action":"output","Package":"example.com/x","Output":"# example.com/x [example.com/x.test]\n"}
{"Action":"output","Package":"example.com/x","Output":"./x_test.go:7:2: undefined: MissingSymbol\n"}
{"Action":"build-fail","Package":"example.com/x"}
{"Action":"fail","Package":"example.com/x","FailedBuild":"example.com/x [example.com/x.test]"}`
	passing, failing, total := parseGoTestJSON(out)
	if passing != 0 {
		t.Errorf("passing=%d, want 0", passing)
	}
	if failing < 1 {
		t.Fatalf("failing=%d, want >=1 (test-compile failure must count as a failure)", failing)
	}
	// build-fail + package fail refer to the SAME package: must not double-count.
	if failing != 1 || total != 1 {
		t.Errorf("failing=%d total=%d, want 1/1 (same package counted once)", failing, total)
	}
}

// TestShouldRunFixCycle_BlocksOnTestCompileFailure ties the parser fix to the
// gate decision: a mainline that builds (production code) but whose tests do
// not compile must NOT be certified complete.
func TestShouldRunFixCycle_BlocksOnTestCompileFailure(t *testing.T) {
	_, failing, _ := parseGoTestJSON(`{"Action":"build-fail","Package":"example.com/x"}`)
	res := VerificationResult{BuildPasses: true, TestsFailing: failing}
	if !ShouldRunFixCycle(res) {
		t.Fatal("gate must require a fix cycle when tests fail to compile, but it passed")
	}
}
