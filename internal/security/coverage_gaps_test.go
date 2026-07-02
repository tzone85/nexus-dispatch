package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Covers is the self-upskilling dedup: it decides whether the agent re-learns a
// vulnerability class it already ships guidance for. These cases pin the three
// match modes (rule ID, CWE alias, miss).
func TestCovers_RuleIDAndCWEAlias(t *testing.T) {
	kb := BaselineKnowledgeBase()

	if !kb.Covers("A03:2021") {
		t.Error("must cover by exact rule ID")
	}
	// A03's CWE field is CWE-89 — a finding classed as CWE-89 is already covered
	// even though no rule has that ID... except the standalone CWE rules do.
	if !kb.Covers("CWE-89") {
		t.Error("must cover a class via a rule's CWE field")
	}
	if !kb.Covers("CWE-918") {
		t.Error("SSRF is baseline (A10's CWE) and must be covered")
	}
	if kb.Covers("CWE-999999") {
		t.Error("unknown class must NOT be covered — that is what triggers learning")
	}
	if kb.Covers("") {
		t.Error("empty class id must not match anything (rules without CWE must not alias it)")
	}
}

func TestCovers_LearnedRuleExtendsCoverage(t *testing.T) {
	kb := BaselineKnowledgeBase()
	if kb.Covers("CWE-1333") {
		t.Fatal("precondition: ReDoS not in baseline")
	}
	grown := kb.Add(VulnRule{ID: "CWE-1333", CWE: "CWE-1333", Title: "ReDoS", Detection: "x", Remediation: "y", Source: RuleLearned})
	if !grown.Covers("CWE-1333") {
		t.Error("a learned rule must extend coverage")
	}
	if kb.Covers("CWE-1333") {
		t.Error("Add must not mutate the receiver")
	}
}

func TestSave_ParentDirCreationFailure(t *testing.T) {
	// A regular file where the parent dir should be makes MkdirAll fail.
	obstruction := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(obstruction, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	kb := BaselineKnowledgeBase()
	err := kb.Save(filepath.Join(obstruction, "sub", "knowledge.json"))
	if err == nil || !strings.Contains(err.Error(), "create knowledge dir") {
		t.Errorf("want create-dir error, got %v", err)
	}
}

func TestSave_WriteFailure(t *testing.T) {
	// Path IS a directory — WriteFile fails after MkdirAll succeeds.
	dir := t.TempDir()
	kb := BaselineKnowledgeBase()
	err := kb.Save(dir)
	if err == nil || !strings.Contains(err.Error(), "write knowledge base") {
		t.Errorf("want write error, got %v", err)
	}
}

func TestLoadKnowledgeBase_CorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKnowledgeBase(path); err == nil || !strings.Contains(err.Error(), "parse knowledge base") {
		t.Errorf("corrupt KB must be a loud error, never silently replaced by the baseline: %v", err)
	}
}

func TestLoadKnowledgeBase_ReadError(t *testing.T) {
	// Path is a directory: ReadFile errors but with something other than
	// IsNotExist — must be surfaced, not treated as first-run.
	if _, err := LoadKnowledgeBase(t.TempDir()); err == nil || !strings.Contains(err.Error(), "read knowledge base") {
		t.Errorf("non-ENOENT read failure must be surfaced: %v", err)
	}
}

func TestDetectLanguages_ManifestSignals(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"rust", map[string]string{"Cargo.toml": ""}, []string{"rust"}},
		{"php", map[string]string{"composer.json": "{}"}, []string{"php"}},
		{"ruby", map[string]string{"Gemfile": ""}, []string{"ruby"}},
		{"python", map[string]string{"pyproject.toml": ""}, []string{"python"}},
		{"js-without-tsconfig", map[string]string{"package.json": "{}"}, []string{"javascript"}},
		{"ts-beats-js", map[string]string{"package.json": "{}", "tsconfig.json": "{}"}, []string{"typescript"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectLanguages(seedRepo(t, tc.files))
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func TestDetectLanguages_ExtensionFallbackAndSkips(t *testing.T) {
	repo := t.TempDir()
	// A .ts source with no manifest establishes typescript by extension; a .js
	// file must then NOT add javascript (the ts/js distinction is respected).
	// node_modules content must be skipped entirely.
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("tsconfig.json", "{}")
	mustWrite("package.json", "{}")
	mustWrite("src/app.js", "x")                   // must not add javascript (ts already set)
	mustWrite("deploy.sh", "#!/bin/sh")            // shell via extension
	mustWrite("node_modules/dep/index.rb", "puts") // must be skipped, no ruby

	got := DetectLanguages(repo)
	set := map[string]bool{}
	for _, l := range got {
		set[l] = true
	}
	if !set["typescript"] || !set["shell"] {
		t.Errorf("want typescript+shell, got %v", got)
	}
	if set["javascript"] {
		t.Errorf("javascript must not be added when typescript is established: %v", got)
	}
	if set["ruby"] {
		t.Errorf("node_modules must be skipped: %v", got)
	}
}

func TestParsers_InvalidJSONIsAnError(t *testing.T) {
	garbage := []byte("PANIC not json")
	if _, err := parseGosec(garbage, "/repo"); err == nil {
		t.Error("parseGosec must surface invalid JSON")
	}
	if _, err := parseGitleaks(garbage, "/repo"); err == nil {
		t.Error("parseGitleaks must surface invalid JSON")
	}
	if _, err := parseSemgrep(garbage, "/repo"); err == nil {
		t.Error("parseSemgrep must surface invalid JSON")
	}
	if _, err := parseNpmAudit(garbage); err == nil {
		t.Error("parseNpmAudit must surface invalid JSON")
	}
}

func TestParseGosec_LineRangeAndMissingCWE(t *testing.T) {
	out := []byte(`{"Issues":[{"severity":"MEDIUM","rule_id":"G304","details":"x","file":"a.go","line":"12-14","cwe":{"id":""}}]}`)
	fs, err := parseGosec(out, "/repo")
	if err != nil || len(fs) != 1 {
		t.Fatalf("parse: %v %v", fs, err)
	}
	if fs[0].Line != 12 {
		t.Errorf("range line must take the start, got %d", fs[0].Line)
	}
	if fs[0].Detail != "" {
		t.Errorf("empty CWE id must not render as 'CWE-', got %q", fs[0].Detail)
	}
}

func TestParseNpmAudit_FallsBackToMapKeyForName(t *testing.T) {
	out := []byte(`{"vulnerabilities":{"left-pad":{"name":"","severity":"low","range":"*","via":[]}}}`)
	fs, err := parseNpmAudit(out)
	if err != nil || len(fs) != 1 {
		t.Fatalf("parse: %v %v", fs, err)
	}
	if fs[0].RuleID != "npm:left-pad" {
		t.Errorf("empty name must fall back to the map key, got %q", fs[0].RuleID)
	}
}

func TestParseGovulncheck_MalformedLinesSkipped(t *testing.T) {
	out := []byte("Vulnerability #1 without colon\nVulnerability #2:   \nVulnerability #3: GO-2025-999\n")
	fs, err := parseGovulncheck(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].RuleID != "GO-2025-999" {
		t.Errorf("malformed lines must be skipped, valid ones kept: %+v", fs)
	}
}

func TestSeverityString_AllValues(t *testing.T) {
	want := map[Severity]string{
		SeverityCritical: "critical",
		SeverityHigh:     "high",
		SeverityMedium:   "medium",
		SeverityLow:      "low",
		SeverityInfo:     "info",
		Severity(42):     "info", // unknown ranks must not invent labels
	}
	for sev, label := range want {
		if got := sev.String(); got != label {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, label)
		}
	}
}

func TestFormatMarkdown_EmptyReportSaysNone(t *testing.T) {
	r := Report{RepoDir: "/x", Languages: []string{"go"}, KBVersion: 1}
	md := r.FormatMarkdown()
	if !strings.Contains(md, "Scanners run: none") {
		t.Errorf("empty scanner lists must render as 'none':\n%s", md)
	}
	if !strings.Contains(md, "0 total") {
		t.Errorf("zero findings must be stated:\n%s", md)
	}
}

func TestRelPath_OutsideRepoKeptVerbatim(t *testing.T) {
	if got := relPath("/repo", "/elsewhere/file.go"); !strings.Contains(got, "elsewhere") {
		t.Errorf("paths outside the repo must not be mangled, got %q", got)
	}
	if got := relPath("/repo", "/repo/pkg/a.go"); got != "pkg/a.go" {
		t.Errorf("in-repo paths become repo-relative, got %q", got)
	}
}
