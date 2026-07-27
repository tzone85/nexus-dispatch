package engine

import "testing"

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
