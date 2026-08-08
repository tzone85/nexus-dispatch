package engine

import "testing"

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
