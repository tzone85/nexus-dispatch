package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func touch(t *testing.T, dir string, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func criteriaValues(p config.ProjectProfile) []string {
	var out []string
	for _, c := range p.Criteria {
		out = append(out, c.Value)
	}
	return out
}

func TestDetectProject_Go(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	p := config.DetectProject(dir)
	if p.Label != "go" {
		t.Fatalf("want go, got %s", p.Label)
	}
	if got := criteriaValues(p); len(got) != 3 || got[0] != "go build ./..." {
		t.Fatalf("unexpected go criteria: %v", got)
	}
}

func TestDetectProject_SwiftPackage(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Package.swift")
	p := config.DetectProject(dir)
	if p.Label != "swift" {
		t.Fatalf("want swift, got %s", p.Label)
	}
	if got := criteriaValues(p); len(got) != 2 || got[0] != "swift build" {
		t.Fatalf("unexpected swift criteria: %v", got)
	}
}

func TestDetectProject_XcodeWithoutSwiftPM(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Memory.xcodeproj/project.pbxproj")
	p := config.DetectProject(dir)
	if p.Label != "swift-xcode" {
		t.Fatalf("want swift-xcode, got %s", p.Label)
	}
	// Building needs full Xcode, which cannot be assumed: no criteria is
	// honest; `go build` criteria would fail every story unconditionally.
	if len(p.Criteria) != 0 {
		t.Fatalf("xcode-only project must have no default criteria, got %v", criteriaValues(p))
	}
}

func TestDetectProject_PHPWithoutVendoredPhpunit(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "composer.json")
	p := config.DetectProject(dir)
	if p.Label != "php" {
		t.Fatalf("want php, got %s", p.Label)
	}
	if len(p.Criteria) != 0 {
		t.Fatalf("php without vendor/bin/phpunit must have no criteria, got %v", criteriaValues(p))
	}
}

func TestDetectProject_Unknown(t *testing.T) {
	p := config.DetectProject(t.TempDir())
	if p.Label != "unknown" || len(p.Criteria) != 0 {
		t.Fatalf("unknown project must carry no criteria, got %s %v", p.Label, criteriaValues(p))
	}
}

func TestDefaultYAMLFor_NonGoNeverGetsGoCriteria(t *testing.T) {
	// Regression: the 2026-08-08 gauntlet run gated a Swift repo on
	// `go build ./...`, which exhausted every story's rejection budget.
	dir := t.TempDir()
	touch(t, dir, "Memory.xcodeproj/project.pbxproj")
	data, label, err := config.DefaultYAMLFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	if label != "swift-xcode" {
		t.Fatalf("want swift-xcode, got %s", label)
	}
	// The command allowlist may legitimately mention go tools; the
	// completion-gating criteria must not.
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.QA.SuccessCriteria) != 0 {
		t.Fatalf("swift-xcode repo must have no default criteria, got %+v", cfg.QA.SuccessCriteria)
	}
}

func TestRuntimeConfig_MaxCriteriaRetriesParsed(t *testing.T) {
	// Regression: the field existed on the runtime but was never wired from
	// YAML, so operators could not raise the self-correction budget before a
	// destructive escalation. It must round-trip through config parsing.
	yamlBody := "native: true\nmax_iterations: 40\nmax_criteria_retries: 5\n"
	var rc config.RuntimeConfig
	if err := yaml.Unmarshal([]byte(yamlBody), &rc); err != nil {
		t.Fatal(err)
	}
	if rc.MaxCriteriaRetries != 5 {
		t.Fatalf("want MaxCriteriaRetries=5 from yaml, got %d", rc.MaxCriteriaRetries)
	}
}

func TestQAConfig_CriteriaAuthoritativeParsed(t *testing.T) {
	// The flag makes objective success_criteria the final word over a small
	// local reviewer model that invents out-of-scope requirements. It must
	// round-trip through YAML and default to false (reviewer keeps its veto).
	var withFlag config.Config
	if err := yaml.Unmarshal([]byte("qa:\n  criteria_authoritative: true\n"), &withFlag); err != nil {
		t.Fatal(err)
	}
	if !withFlag.QA.CriteriaAuthoritative {
		t.Fatal("expected criteria_authoritative=true from yaml")
	}
	def := config.DefaultConfig()
	if def.QA.CriteriaAuthoritative {
		t.Fatal("criteria_authoritative must default to false (reviewer veto preserved)")
	}
}
