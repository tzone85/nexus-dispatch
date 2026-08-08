package config

import (
	"os"
	"path/filepath"
)

// ProjectProfile describes the toolchain detected in a repository and the
// success criteria appropriate to it. Criteria that gate agent completion
// must match the project's actual language: a Swift or PHP repository gated
// on `go build` can never pass, which silently breaks the pipeline's core
// promise on non-Go projects.
type ProjectProfile struct {
	// Label names the detected toolchain ("go", "swift", ...) or "unknown".
	Label string
	// Criteria are the language-appropriate completion gates. Empty for
	// unknown projects — no criteria is honest; wrong-language criteria are
	// an unconditional failure.
	Criteria []SuccessCriterion
	// AllowlistExtras are commands the detected toolchain needs agents to
	// be able to run, appended to the runtime command allowlist.
	AllowlistExtras []string
}

// DetectProject inspects dir for well-known project markers and returns the
// matching profile. Markers are checked in priority order; the first match
// wins. An unrecognized directory yields Label "unknown" with no criteria.
func DetectProject(dir string) ProjectProfile {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	anyGlob := func(pattern string) bool {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		return len(matches) > 0
	}

	switch {
	case has("go.mod"):
		return ProjectProfile{
			Label: "go",
			Criteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "go build ./..."},
				{Kind: "command_succeeds", Value: "go vet ./..."},
				{Kind: "test_passes", Value: "go test ./..."},
			},
			AllowlistExtras: []string{"go build ./...", "go vet ./...", "go test ./..."},
		}
	case has("Package.swift"):
		return ProjectProfile{
			Label: "swift",
			Criteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "swift build"},
				{Kind: "test_passes", Value: "swift test"},
			},
			AllowlistExtras: []string{"swift build", "swift test"},
		}
	case has("Cargo.toml"):
		return ProjectProfile{
			Label: "rust",
			Criteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "cargo build"},
				{Kind: "test_passes", Value: "cargo test"},
			},
			AllowlistExtras: []string{"cargo build", "cargo test"},
		}
	case has("package.json"):
		return ProjectProfile{
			Label: "node",
			Criteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "npm run build --if-present"},
				{Kind: "test_passes", Value: "npm test"},
			},
			AllowlistExtras: []string{"npm run build --if-present", "npm test", "npm ci"},
		}
	case has("pyproject.toml"):
		return ProjectProfile{
			Label: "python",
			Criteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "python3 -m compileall -q ."},
			},
			AllowlistExtras: []string{"python3 -m compileall -q .", "python3 -m pytest"},
		}
	case has("composer.json"):
		p := ProjectProfile{
			Label:           "php",
			AllowlistExtras: []string{"php -l", "php tests/run.php", "composer validate"},
		}
		// Only gate on phpunit when it is actually installed; legacy PHP
		// projects rarely have a runnable vendor/ on a modern machine.
		if has(filepath.Join("vendor", "bin", "phpunit")) {
			p.Criteria = []SuccessCriterion{{Kind: "test_passes", Value: "vendor/bin/phpunit"}}
		}
		return p
	case anyGlob("*.xcodeproj"):
		// Xcode project without SwiftPM: building needs full Xcode, which
		// cannot be assumed. No criteria — agents are gated by review/QA,
		// and the operator can add criteria once a Package.swift exists.
		return ProjectProfile{Label: "swift-xcode", AllowlistExtras: []string{"swift build", "swift test"}}
	default:
		return ProjectProfile{Label: "unknown"}
	}
}
