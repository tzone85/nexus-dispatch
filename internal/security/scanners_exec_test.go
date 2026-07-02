package security

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeTool installs an executable shell script named bin into dir that prints
// output and exits with code (verbatim — scanners use non-zero exits to mean
// "findings present", and some distinguish exit 2). The heredoc sentinel is
// collision-resistant so canned output can never terminate it early.
func fakeTool(t *testing.T, dir, bin, output string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-tool harness is POSIX-shell based")
	}
	const sentinel = "FAKE_SCANNER_OUTPUT_BOUNDARY_9f2c1d"
	if strings.Contains(output, sentinel) {
		t.Fatalf("canned output collides with the heredoc sentinel %s", sentinel)
	}
	script := "#!/bin/sh\ncat <<'" + sentinel + "'\n" + output + "\n" + sentinel + "\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// seedRepo creates a repo dir whose manifests establish the given languages.
func seedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const fakeGosecOut = `{"Issues":[{"severity":"HIGH","rule_id":"G101","details":"Potential hardcoded credentials","file":"main.go","line":"7","cwe":{"id":"798"}}]}`

const fakeGitleaksOut = `[{"Description":"AWS access key","File":"config.env","StartLine":3,"RuleID":"aws-access-key-id"}]`

const fakeSemgrepOut = `{"results":[{"check_id":"go.lang.security.audit.sqli","path":"db.go","start":{"line":42},"extra":{"message":"SQL built from input","severity":"ERROR","metadata":{"cwe":["CWE-89"],"owasp":["A03:2021 - Injection"]}}}]}`

const fakeGovulncheckOut = `Scanning your code and 42 packages across 7 dependent modules for known vulnerabilities...

Vulnerability #1: GO-2024-1234
    A bad thing in some module.`

const fakeNpmAuditOut = `{"vulnerabilities":{"lodash":{"name":"lodash","severity":"high","range":"<4.17.21","via":[]}}}`

// installAllFakeScanners provisions every registry binary as a fake and points
// PATH at only that directory. Returns the bin dir for per-test overrides.
func installAllFakeScanners(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	fakeTool(t, bin, "gosec", fakeGosecOut, 1)
	fakeTool(t, bin, "gitleaks", fakeGitleaksOut, 1)
	fakeTool(t, bin, "semgrep", fakeSemgrepOut, 0)
	fakeTool(t, bin, "govulncheck", fakeGovulncheckOut, 1)
	fakeTool(t, bin, "npm", fakeNpmAuditOut, 1)
	// Keep /bin:/usr/bin so the fake scripts can find cat; no real security
	// scanner is ever installed there, so LookPath still resolves only fakes.
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	return bin
}

func TestRunScanners_EndToEnd_AllToolsParse(t *testing.T) {
	installAllFakeScanners(t)
	repo := seedRepo(t, map[string]string{
		"go.mod":        "module example.com/x\n",
		"package.json":  "{}",
		"tsconfig.json": "{}",
	})

	findings, ran, skipped, failed := RunScanners(context.Background(), repo)

	if len(skipped) != 0 {
		t.Errorf("all tools installed — skipped must be empty, got %v", skipped)
	}
	if len(failed) != 0 {
		t.Errorf("all tools parse — failed must be empty, got %v", failed)
	}
	if len(ran) != 5 {
		t.Errorf("go+typescript repo makes all 5 scanners applicable, ran=%v", ran)
	}

	byTool := map[string]Finding{}
	for _, f := range findings {
		byTool[f.Tool] = f
	}
	if f := byTool["gosec"]; f.RuleID != "G101" || f.Severity != SeverityHigh || f.Line != 7 || f.Detail != "CWE-798" {
		t.Errorf("gosec finding mis-parsed: %+v", f)
	}
	if f := byTool["gitleaks"]; f.Severity != SeverityCritical || f.File != "config.env" {
		t.Errorf("gitleaks finding mis-parsed: %+v", f)
	}
	if f := byTool["semgrep"]; f.Severity != SeverityHigh || f.Detail != "CWE-89" || f.Category == "" {
		t.Errorf("semgrep finding mis-parsed: %+v", f)
	}
	if f := byTool["govulncheck"]; f.RuleID != "GO-2024-1234" {
		t.Errorf("govulncheck finding mis-parsed: %+v", f)
	}
	if f := byTool["npm-audit"]; f.RuleID != "npm:lodash" || f.Severity != SeverityHigh {
		t.Errorf("npm-audit finding mis-parsed: %+v", f)
	}
}

func TestRunScanners_FailedToolIsReportedNotSwallowed(t *testing.T) {
	bin := installAllFakeScanners(t)
	// gosec now emits garbage — a parse failure must land in `failed`, never in
	// `ran`: a tool that failed to inspect the code must be distinguishable
	// from one that found nothing.
	fakeTool(t, bin, "gosec", "PANIC: not json", 1)
	repo := seedRepo(t, map[string]string{"go.mod": "module example.com/x\n"})

	findings, ran, skipped, failed := RunScanners(context.Background(), repo)

	if len(failed) != 1 || failed[0] != ScannerGosec {
		t.Fatalf("want failed=[gosec], got %v", failed)
	}
	for _, k := range ran {
		if k == ScannerGosec {
			t.Error("a failed scanner must not be counted as ran")
		}
	}
	if len(skipped) != 0 {
		t.Errorf("nothing should be skipped, got %v", skipped)
	}
	// The other applicable tools still contribute findings (graceful degradation).
	found := map[string]bool{}
	for _, f := range findings {
		found[f.Tool] = true
	}
	if !found["gitleaks"] || !found["govulncheck"] {
		t.Errorf("other tools must keep scanning after one fails, got %v", found)
	}
}

func TestRunScanners_MissingToolsSkippedVisibly(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "gitleaks", fakeGitleaksOut, 1) // only gitleaks installed
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	repo := seedRepo(t, map[string]string{"go.mod": "module example.com/x\n"})

	_, ran, skipped, failed := RunScanners(context.Background(), repo)

	if len(ran) != 1 || ran[0] != ScannerGitleaks {
		t.Errorf("want ran=[gitleaks], got %v", ran)
	}
	skippedSet := map[ScannerKind]bool{}
	for _, k := range skipped {
		skippedSet[k] = true
	}
	// Applicable-but-missing for a Go repo: semgrep (all langs), gosec, govulncheck.
	for _, want := range []ScannerKind{ScannerSemgrep, ScannerGosec, ScannerGovulncheck} {
		if !skippedSet[want] {
			t.Errorf("missing tool %s must be reported as skipped, got %v", want, skipped)
		}
	}
	// npm-audit is NOT applicable (no js/ts) — it must not appear anywhere.
	if skippedSet[ScannerNpmAudit] {
		t.Errorf("inapplicable scanner must not be listed as skipped: %v", skipped)
	}
	if len(failed) != 0 {
		t.Errorf("nothing ran and errored, failed must be empty: %v", failed)
	}
}

func TestRunScanners_DedupesIdenticalFindings(t *testing.T) {
	bin := t.TempDir()
	// gitleaks reports the same secret twice (same rule, file, line).
	dup := `[{"Description":"AWS access key","File":"config.env","StartLine":3,"RuleID":"aws-access-key-id"},
	        {"Description":"AWS access key","File":"config.env","StartLine":3,"RuleID":"aws-access-key-id"}]`
	fakeTool(t, bin, "gitleaks", dup, 1)
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	repo := seedRepo(t, map[string]string{"README.md": "x"})

	findings, _, _, _ := RunScanners(context.Background(), repo)
	if len(findings) != 1 {
		t.Errorf("identical findings must be deduped, got %d", len(findings))
	}
}

func TestScannerRun_UnknownKindIsNoOp(t *testing.T) {
	s := Scanner{Kind: ScannerKind("bogus"), Bin: "bogus"}
	findings, err := s.Run(context.Background(), t.TempDir())
	if findings != nil || err != nil {
		t.Errorf("unknown kind must be a no-op, got %v / %v", findings, err)
	}
}

func TestScannerRun_PerKindDispatch(t *testing.T) {
	installAllFakeScanners(t)
	repo := seedRepo(t, map[string]string{"go.mod": "module x\n"})

	// Note: Scanner.Run dispatches on Kind with hardcoded binary names and
	// never reads Bin — Bin serves only the LookPath availability checks in
	// RunScanners/DetectScanners. Bin values here mirror allScanners() (the
	// npm-audit scanner execs "npm", not "npm-audit").
	cases := []struct {
		kind     ScannerKind
		bin      string
		wantTool string
	}{
		{ScannerGosec, "gosec", "gosec"},
		{ScannerGitleaks, "gitleaks", "gitleaks"},
		{ScannerSemgrep, "semgrep", "semgrep"},
		{ScannerGovulncheck, "govulncheck", "govulncheck"},
		{ScannerNpmAudit, "npm", "npm-audit"},
	}
	for _, tc := range cases {
		fs, err := Scanner{Kind: tc.kind, Bin: tc.bin}.Run(context.Background(), repo)
		if err != nil {
			t.Errorf("%s: %v", tc.kind, err)
			continue
		}
		if len(fs) != 1 || fs[0].Tool != tc.wantTool {
			t.Errorf("%s: want one %s finding, got %+v", tc.kind, tc.wantTool, fs)
		}
	}
}

func TestDetectScanners_CombinesLanguageAndAvailability(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "gosec", "", 0)
	fakeTool(t, bin, "npm", "", 0)
	t.Setenv("PATH", bin+":/bin:/usr/bin")

	// Go-only repo: gosec applies and is installed; npm is installed but not
	// applicable; gitleaks/semgrep apply but are missing.
	repo := seedRepo(t, map[string]string{"go.mod": "module x\n"})
	got := DetectScanners(repo)
	if len(got) != 1 || got[0].Kind != ScannerGosec {
		t.Errorf("want [gosec], got %v", got)
	}
}

func TestKnownScanners_RegistryComplete(t *testing.T) {
	known := KnownScanners()
	if len(known) != 5 {
		t.Fatalf("registry drifted: want 5 scanners, got %d", len(known))
	}
	bins := map[string]bool{}
	for _, s := range known {
		bins[s.Bin] = true
	}
	for _, want := range []string{"gosec", "govulncheck", "gitleaks", "semgrep", "npm"} {
		if !bins[want] {
			t.Errorf("registry missing %s", want)
		}
	}
}

func TestInstallHint_EveryRegistryBinHasOne(t *testing.T) {
	for _, s := range KnownScanners() {
		if InstallHint(s.Bin) == "" {
			t.Errorf("no install hint for %s — the preflight message would be blank", s.Bin)
		}
	}
	if InstallHint("made-up-tool") != "" {
		t.Error("unknown binary must return empty hint")
	}
}
