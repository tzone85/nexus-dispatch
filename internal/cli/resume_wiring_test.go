package cli

import (
	"os"
	"strings"
	"testing"
)

// TestResume_WiresTechLeadFixer guards against a dead-wire regression: the
// post-merge integration-build feature (WithMonTechLeadFixer + TechLeadFixer)
// was fully implemented and unit-tested, but runResume never wired the fixer
// into the monitor, so the stage never ran in production. The option's own
// wiring test could not catch this. This test scans the resume source to
// confirm the fixer is actually constructed and attached.
func TestResume_WiresTechLeadFixer(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)

	for _, want := range []string{"NewTechLeadFixer(", "WithMonTechLeadFixer("} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the post-merge integration fixer: missing %q", want)
		}
	}
}
