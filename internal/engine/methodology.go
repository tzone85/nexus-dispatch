package engine

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// codeExtensionsRequiringTests are file extensions for which TDD enforcement
// expects a paired test file to live in the same story's owned_files.
var codeExtensionsRequiringTests = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rb": true, ".java": true, ".kt": true, ".rs": true,
	".swift": true, ".cs": true, ".scala": true,
}

// testFilePatterns are filename substrings / suffixes that count as a test.
// A story's owned_files satisfies the TDD pairing rule if any file matches.
var testFilePatterns = []string{
	"_test.go", ".test.ts", ".test.tsx", ".test.js", ".test.jsx",
	".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
	"_test.py", "_spec.py", "test_", "_test.rb", "_spec.rb",
	"Test.java", "Tests.java", "Spec.java",
	"_test.rs",
}

// storyOwnsCodeWithoutTest reports whether a story owns at least one
// source-code file but no matching test file. Used for TDD enforcement
// warnings. Stories owning only config/markdown/lock files are exempt.
func storyOwnsCodeWithoutTest(s PlannedStory) bool {
	hasCode := false
	hasTest := false
	for _, f := range s.OwnedFiles {
		base := filepath.Base(f)
		// Test detection.
		for _, pat := range testFilePatterns {
			if strings.Contains(base, pat) {
				hasTest = true
				break
			}
		}
		// Code detection (separate from test — a *_test.go is not a code-needing-test file).
		ext := strings.ToLower(filepath.Ext(f))
		if codeExtensionsRequiringTests[ext] {
			isTest := false
			for _, pat := range testFilePatterns {
				if strings.Contains(base, pat) {
					isTest = true
					break
				}
			}
			if !isTest {
				hasCode = true
			}
		}
	}
	return hasCode && !hasTest
}

// methodologyOverridePattern matches "methodology: <value>" in requirement
// text. Supported values:
//
//	default | strict   → DDD + TDD ON (canonical default)
//	relaxed | none     → DDD + TDD OFF
//	tdd-only           → TDD on, DDD off
//	ddd-only           → DDD on, TDD off
//
// The regex is case-insensitive and tolerates whitespace variations.
var methodologyOverridePattern = regexp.MustCompile(`(?i)methodology\s*:\s*([a-z][a-z0-9-]*)`)

// MethodologyDecision captures the effective DDD/TDD settings for a
// requirement after considering config defaults and override directives.
type MethodologyDecision struct {
	DDD    bool
	TDD    bool
	Source string // "config", "override:relaxed", "override:tdd-only", etc.
}

// ResolveMethodology returns the effective methodology for a requirement.
// It honors the override directive in the requirement text only when
// cfg.AllowOverride is true.
func ResolveMethodology(cfg config.MethodologyConfig, requirement string) MethodologyDecision {
	d := MethodologyDecision{
		DDD:    cfg.DDD,
		TDD:    cfg.TDD,
		Source: "config",
	}

	if !cfg.AllowOverride {
		return d
	}

	match := methodologyOverridePattern.FindStringSubmatch(requirement)
	if len(match) < 2 {
		return d
	}
	val := strings.ToLower(match[1])
	switch val {
	case "relaxed", "none", "off":
		d.DDD = false
		d.TDD = false
		d.Source = "override:" + val
	case "tdd-only":
		d.DDD = false
		d.TDD = true
		d.Source = "override:" + val
	case "ddd-only":
		d.DDD = true
		d.TDD = false
		d.Source = "override:" + val
	case "default", "strict":
		d.DDD = true
		d.TDD = true
		d.Source = "override:" + val
	}
	return d
}

// buildMethodologyDirective produces the planner prompt addendum that
// captures DDD/TDD requirements. Returns "" when both are off.
func buildMethodologyDirective(cfg config.MethodologyConfig, requirement string) string {
	dec := ResolveMethodology(cfg, requirement)
	if !dec.DDD && !dec.TDD {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Mandatory methodology\n\n")
	b.WriteString("(source: " + dec.Source + ")\n\n")

	if dec.DDD {
		b.WriteString(`### Domain-Driven Design

- Separate the domain layer from infrastructure. Pure-function entities and value
  objects live in a domain package (e.g. internal/<domain>/, src/domain/, app/core/).
  Infrastructure adapters (DB, HTTP, filesystem, external APIs) live elsewhere
  and depend on the domain — never the reverse.
- Establish a ubiquitous language: vocabulary used in code MUST match the
  vocabulary in the requirement. If the requirement says "ApplyMove" do not
  introduce a synonym like "PlayTurn" without a documented reason.
- Each story should target ONE bounded context. If a story spans the domain
  AND infrastructure layers, split it.
- Anti-corruption layers (mappers, adapters) are explicit, not implicit.

`)
	}

	if dec.TDD {
		b.WriteString(`### Test-Driven Development

- Every story that adds behavior MUST own its test file. owned_files MUST
  include both the implementation file AND the matching *_test.go (or
  language-equivalent test file).
- Tests are written FIRST. The story's description should explicitly say
  "write a failing test asserting X, then implement to pass it."
- Table-driven tests preferred for any function with branching logic.
- Coverage gate: stories that add >25 lines of new code must achieve at
  least the configured coverage minimum on the touched files. Stories that
  add infra-only changes (config, vendored deps) are exempt.
- Refactor stories MUST point to existing tests they keep green; if no test
  exists for the refactor target, the FIRST commit in the story must
  characterize-test the existing behavior.

`)
	}

	b.WriteString("If the user explicitly states `methodology: relaxed` or `methodology: none` ")
	b.WriteString("in their requirement, ignore this section.\n")
	return b.String()
}
