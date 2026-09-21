package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ScannerKind identifies a security scanner the agent can orchestrate.
type ScannerKind string

const (
	ScannerSemgrep     ScannerKind = "semgrep"     // multi-language SAST
	ScannerGosec       ScannerKind = "gosec"       // Go SAST
	ScannerGovulncheck ScannerKind = "govulncheck" // Go dependency CVEs
	ScannerGitleaks    ScannerKind = "gitleaks"    // secret scanning (all langs)
	ScannerNpmAudit    ScannerKind = "npm-audit"   // Node dependency CVEs
)

// scannerTimeout bounds a single scanner invocation.
const scannerTimeout = 4 * time.Minute

// Scanner describes a tool: the PATH binary that gates availability and the
// languages it applies to (empty = all languages).
type Scanner struct {
	Kind      ScannerKind
	Bin       string
	Languages []string
}

// allScanners is the registry of scanners the agent knows how to run.
func allScanners() []Scanner {
	return []Scanner{
		{Kind: ScannerGitleaks, Bin: "gitleaks"}, // secrets — every language
		{Kind: ScannerSemgrep, Bin: "semgrep"},   // multi-language SAST
		{Kind: ScannerGosec, Bin: "gosec", Languages: []string{"go"}},
		{Kind: ScannerGovulncheck, Bin: "govulncheck", Languages: []string{"go"}},
		{Kind: ScannerNpmAudit, Bin: "npm", Languages: []string{"javascript", "typescript"}},
	}
}

func langMatch(scannerLangs, repoLangs []string) bool {
	if len(scannerLangs) == 0 {
		return true
	}
	for _, a := range scannerLangs {
		for _, b := range repoLangs {
			if strings.EqualFold(a, b) {
				return true
			}
		}
	}
	return false
}

// applicableScanners returns the scanners that are both relevant to the repo's
// languages and present in PATH (per the available set, keyed by Bin).
func applicableScanners(langs []string, available map[string]bool) []Scanner {
	var out []Scanner
	for _, s := range allScanners() {
		if !available[s.Bin] {
			continue
		}
		if !langMatch(s.Languages, langs) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// RunScanners runs every applicable+available scanner against repoDir and
// returns deduped findings, the scanners that ran clean, the applicable
// scanners that were skipped because they are not installed, and the scanners
// that ran but errored (exec crash, timeout, parse failure). A scanner failing
// never aborts the scan, but its failure is NOT silently swallowed: it is
// logged and reported in `failed` rather than counted as a clean run —
// otherwise a tool that failed to inspect the code is indistinguishable from
// one that found nothing, and the security gate would report a build as
// scanned-clean when coverage was actually lost.
func RunScanners(ctx context.Context, repoDir string) (findings []Finding, ran, skipped, failed []ScannerKind) {
	langs := DetectLanguages(repoDir)
	available := map[string]bool{}
	for _, s := range allScanners() {
		if _, err := exec.LookPath(s.Bin); err == nil {
			available[s.Bin] = true
		}
	}
	for _, s := range allScanners() {
		if !langMatch(s.Languages, langs) {
			continue
		}
		if !available[s.Bin] {
			skipped = append(skipped, s.Kind)
			continue
		}
		fs, err := s.Run(ctx, repoDir)
		if err != nil {
			// Graceful degradation: keep scanning with the other tools, but make
			// the coverage loss visible — a failed scan must never masquerade as
			// a clean one.
			failed = append(failed, s.Kind)
			log.Printf("[security] scanner %s failed (coverage lost for this tool): %v", s.Kind, err)
			continue
		}
		ran = append(ran, s.Kind)
		findings = append(findings, fs...)
	}
	return DedupeFindings(findings), ran, skipped, failed
}

// KnownScanners returns the full scanner registry regardless of PATH
// availability or repo languages, so other packages can report on missing
// tools without duplicating the list.
func KnownScanners() []Scanner {
	return allScanners()
}

// InstallHint returns the install command for a scanner binary, or "" when no
// hint is known. Hints target macOS/Homebrew and the Go toolchain.
func InstallHint(bin string) string {
	switch bin {
	case "gosec":
		return "go install github.com/securego/gosec/v2/cmd/gosec@latest"
	case "govulncheck":
		return "go install golang.org/x/vuln/cmd/govulncheck@latest"
	case "gitleaks":
		return "brew install gitleaks"
	case "semgrep":
		return "brew install semgrep"
	case "npm":
		return "brew install node"
	default:
		return ""
	}
}

// DetectScanners returns the scanners applicable to repoDir and available on the
// host. Detection combines language inspection with exec.LookPath.
func DetectScanners(repoDir string) []Scanner {
	langs := DetectLanguages(repoDir)
	available := map[string]bool{}
	for _, s := range allScanners() {
		if _, err := exec.LookPath(s.Bin); err == nil {
			available[s.Bin] = true
		}
	}
	return applicableScanners(langs, available)
}

// relPath makes an absolute scanner path repo-relative for stable, readable
// findings. Paths already relative (or outside repoDir) are returned cleaned.
func relPath(repoDir, p string) string {
	if rel, err := filepath.Rel(repoDir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// ---- Parsers (pure: tool output → findings) -------------------------------

func parseGosec(out []byte, repoDir string) ([]Finding, error) {
	var doc struct {
		Issues []struct {
			Severity string `json:"severity"`
			RuleID   string `json:"rule_id"`
			Details  string `json:"details"`
			File     string `json:"file"`
			Line     string `json:"line"`
			CWE      struct {
				ID string `json:"id"`
			} `json:"cwe"`
		} `json:"Issues"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(doc.Issues))
	for _, i := range doc.Issues {
		line, _ := strconv.Atoi(strings.SplitN(i.Line, "-", 2)[0]) // gosec may emit "12-14"
		cwe := ""
		if i.CWE.ID != "" {
			cwe = "CWE-" + i.CWE.ID
		}
		findings = append(findings, Finding{
			Tool:     "gosec",
			RuleID:   i.RuleID,
			Severity: ParseSeverity(i.Severity),
			File:     relPath(repoDir, i.File),
			Line:     line,
			Title:    i.Details,
			Detail:   cwe,
			Source:   "scanner",
		})
	}
	return findings, nil
}

func parseGitleaks(out []byte, repoDir string) ([]Finding, error) {
	var rows []struct {
		Description string `json:"Description"`
		File        string `json:"File"`
		StartLine   int    `json:"StartLine"`
		RuleID      string `json:"RuleID"`
	}
	if err := json.Unmarshal(extractJSONArray(out), &rows); err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(rows))
	for _, r := range rows {
		findings = append(findings, Finding{
			Tool:     "gitleaks",
			RuleID:   r.RuleID,
			Severity: SeverityCritical, // a committed live secret is always critical
			File:     relPath(repoDir, r.File),
			Line:     r.StartLine,
			Title:    r.Description,
			Detail:   "Hardcoded secret detected (CWE-798)",
			Category: "Cryptographic Failures",
			Source:   "scanner",
		})
	}
	return findings, nil
}

// ansiEscape matches ANSI SGR/CSI escape sequences (e.g. "\x1b[90m").
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// extractJSONArray returns the outermost JSON array in out. Gitleaks (and
// other colourising tools) interleave ANSI-coloured log lines with the JSON
// report on the same stream; escape sequences are stripped first (they
// contain '[' themselves), then the payload is sliced from the first '[' to
// the last ']'. Returns the stripped output unchanged when no array brackets
// exist so the caller still surfaces a parse error with the real content.
func extractJSONArray(out []byte) []byte {
	clean := ansiEscape.ReplaceAll(out, nil)
	start := bytes.IndexByte(clean, '[')
	end := bytes.LastIndexByte(clean, ']')
	if start == -1 || end == -1 || end < start {
		return clean
	}
	return clean[start : end+1]
}

func parseSemgrep(out []byte, repoDir string) ([]Finding, error) {
	var doc struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			Extra struct {
				Message  string `json:"message"`
				Severity string `json:"severity"`
				Metadata struct {
					CWE   []string `json:"cwe"`
					OWASP []string `json:"owasp"`
				} `json:"metadata"`
			} `json:"extra"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(doc.Results))
	for _, r := range doc.Results {
		cwe := ""
		if len(r.Extra.Metadata.CWE) > 0 {
			cwe = r.Extra.Metadata.CWE[0]
		}
		cat := ""
		if len(r.Extra.Metadata.OWASP) > 0 {
			cat = r.Extra.Metadata.OWASP[0]
		}
		findings = append(findings, Finding{
			Tool:     "semgrep",
			RuleID:   r.CheckID,
			Severity: ParseSeverity(r.Extra.Severity),
			File:     relPath(repoDir, r.Path),
			Line:     r.Start.Line,
			Title:    r.Extra.Message,
			Detail:   cwe,
			Category: cat,
			Source:   "scanner",
		})
	}
	return findings, nil
}

func parseNpmAudit(out []byte) ([]Finding, error) {
	var doc struct {
		Vulnerabilities map[string]struct {
			Name     string            `json:"name"`
			Severity string            `json:"severity"`
			Range    string            `json:"range"`
			Via      []json.RawMessage `json:"via"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(doc.Vulnerabilities))
	for pkg, v := range doc.Vulnerabilities {
		name := v.Name
		if name == "" {
			name = pkg
		}
		findings = append(findings, Finding{
			Tool:     "npm-audit",
			RuleID:   "npm:" + name,
			Severity: ParseSeverity(v.Severity),
			File:     "package.json",
			Title:    "Vulnerable dependency: " + name + " " + v.Range,
			Detail:   "Known advisory in dependency " + name,
			Category: "Vulnerable and Outdated Components",
			Source:   "scanner",
		})
	}
	return findings, nil
}

func parseGovulncheck(out []byte) ([]Finding, error) {
	var findings []Finding
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Lines look like: "Vulnerability #1: GO-2024-1234"
		if !strings.HasPrefix(line, "Vulnerability #") {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		id := strings.TrimSpace(line[idx+1:])
		if id == "" {
			continue
		}
		findings = append(findings, Finding{
			Tool:     "govulncheck",
			RuleID:   id,
			Severity: SeverityHigh,
			File:     "go.mod",
			Title:    "Called vulnerability " + id,
			Detail:   "Dependency CVE reachable from your code (https://pkg.go.dev/vuln/" + id + ")",
			Category: "Vulnerable and Outdated Components",
			Source:   "scanner",
		})
	}
	return findings, sc.Err()
}

// Run executes the scanner against repoDir and returns parsed findings. A
// non-zero exit is expected (most scanners exit non-zero when they find issues),
// so output is parsed regardless of exit code; a parse error is returned so the
// caller can log and continue (graceful degradation — one tool failing never
// aborts the scan).
func (s Scanner) Run(ctx context.Context, repoDir string) ([]Finding, error) {
	ctx, cancel := context.WithTimeout(ctx, scannerTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch s.Kind {
	case ScannerGosec:
		cmd = exec.CommandContext(ctx, "gosec", "-fmt=json", "-quiet", "./...")
	case ScannerGovulncheck:
		cmd = exec.CommandContext(ctx, "govulncheck", "./...")
	case ScannerGitleaks:
		cmd = exec.CommandContext(ctx, "gitleaks", "detect", "--no-banner", "--report-format", "json", "--report-path", "/dev/stdout")
	case ScannerSemgrep:
		cmd = exec.CommandContext(ctx, "semgrep", "scan", "--config", "auto", "--json", "--quiet")
	case ScannerNpmAudit:
		cmd = exec.CommandContext(ctx, "npm", "audit", "--json")
	default:
		return nil, nil
	}
	cmd.Dir = repoDir
	// Capture stdout only: scanners emit their machine-readable report on
	// stdout and human log lines (often ANSI-coloured) on stderr. Combining
	// the streams corrupted the JSON payload. A non-zero exit is expected for
	// the JSON scanners (they exit non-zero when they find issues), so we do
	// not treat exit code alone as failure. But a clean parse yielding zero
	// findings is NOT proof of a clean scan: gosec, semgrep and npm audit all
	// emit a well-formed JSON *error* report with an empty result set when they
	// fail to analyze the code (npm audit with no lockfile → {"error":{...}};
	// semgrep offline rule-fetch failure → {"errors":[...],"results":[]}; gosec
	// build/load failure → {"Golang errors":{...},"Issues":null}). Those parse
	// without error, so a per-tool structured-error check routes them to the
	// `failed` list instead of masquerading as clean (see *Result helpers).
	// govulncheck is a further exception — its text output is empty both when it
	// finds nothing AND when it never ran (offline, no go.mod, load error), so a
	// clean parse cannot tell the two apart and we must inspect its exit code.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	out := stdout.Bytes()

	switch s.Kind {
	case ScannerGosec:
		return gosecResult(out, repoDir)
	case ScannerGovulncheck:
		// Exit 0 (no vulnerabilities) and exit 3 (vulnerabilities found) are
		// both successful analyses. Any other outcome — exit 1 (load/network/
		// config error, e.g. NXD's offline-first host cannot reach vuln.go.dev),
		// exit 2 (usage), or a non-exit failure such as a timeout — means the
		// scan never inspected the code. Returning an error here routes it to
		// RunScanners' `failed` list instead of masquerading as a clean run,
		// which is the whole point of that list.
		if !govulncheckCompleted(runErr) {
			// Diagnostics land on stderr with the split streams, so scan
			// stdout first (report text) and fall back to stderr.
			detail := append(append([]byte{}, out...), stderr.Bytes()...)
			return nil, fmt.Errorf("govulncheck did not complete (dependency-CVE coverage lost): %s", scannerFailureDetail(detail, runErr))
		}
		return parseGovulncheck(out)
	case ScannerGitleaks:
		return parseGitleaks(out, repoDir)
	case ScannerSemgrep:
		return semgrepResult(out, repoDir)
	case ScannerNpmAudit:
		return npmAuditResult(out)
	default:
		return nil, nil
	}
}

// gosecResult parses gosec output and, when the scan produced no findings,
// checks gosec's "Golang errors" channel: a populated map with no issues means
// gosec could not build/load the code, so it inspected nothing. That is coverage
// loss, not a clean run, and must be routed to RunScanners' `failed` list. When
// findings ARE present they are returned as-is (partial coverage still surfaces
// real issues rather than dropping them).
func gosecResult(out []byte, repoDir string) ([]Finding, error) {
	findings, err := parseGosec(out, repoDir)
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		if reason := gosecRunError(out); reason != "" {
			return nil, fmt.Errorf("gosec did not complete (Go SAST coverage lost): %s", reason)
		}
	}
	return findings, nil
}

// semgrepResult parses semgrep output and, when no findings were produced,
// inspects semgrep's `errors` array. A fatal error there (e.g. an offline
// `--config auto` rule fetch that cannot reach the registry — the common case on
// the offline-first hosts NXD targets) with an empty result set means semgrep
// scanned nothing, so it is reported as failed rather than clean.
func semgrepResult(out []byte, repoDir string) ([]Finding, error) {
	findings, err := parseSemgrep(out, repoDir)
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		if reason := semgrepRunError(out); reason != "" {
			return nil, fmt.Errorf("semgrep did not complete (SAST coverage lost): %s", reason)
		}
	}
	return findings, nil
}

// npmAuditResult parses npm audit output and, when no vulnerabilities were
// produced, inspects npm's top-level `error` object. npm audit writes that
// object and exits non-zero when the audit itself failed (no lockfile → ENOLOCK,
// no network → ENETUNREACH); the absent `vulnerabilities` key otherwise parses
// as a clean run. Detecting the error object keeps a failed audit out of the
// clean `ran` list.
func npmAuditResult(out []byte) ([]Finding, error) {
	findings, err := parseNpmAudit(out)
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		if reason := npmAuditRunError(out); reason != "" {
			return nil, fmt.Errorf("npm audit did not complete (dependency-CVE coverage lost): %s", reason)
		}
	}
	return findings, nil
}

// gosecRunError returns a non-empty reason when gosec self-reported build/load
// errors (its "Golang errors" map). Returns "" when the map is absent/empty or
// the output is not valid JSON (parseGosec already surfaces a parse error).
func gosecRunError(out []byte) string {
	var doc struct {
		GolangErrors map[string]json.RawMessage `json:"Golang errors"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return ""
	}
	if len(doc.GolangErrors) == 0 {
		return ""
	}
	files := make([]string, 0, len(doc.GolangErrors))
	for f := range doc.GolangErrors {
		files = append(files, f)
	}
	sort.Strings(files)
	return fmt.Sprintf("gosec reported build/load errors for %d file(s): %s", len(files), strings.Join(files, ", "))
}

// semgrepRunError returns a non-empty reason when semgrep's `errors` array
// carries a fatal error (level "error", or an unlabelled entry — conservatively
// treated as fatal). Benign per-file notices ("warn"/"info") do not count as
// coverage loss. Returns "" when there is no such error or the output is not
// valid JSON.
func semgrepRunError(out []byte) string {
	var doc struct {
		Errors []struct {
			Message string `json:"message"`
			Level   string `json:"level"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return ""
	}
	for _, e := range doc.Errors {
		if e.Level != "" && !strings.EqualFold(e.Level, "error") {
			continue // warn/info: not a coverage-losing failure
		}
		if e.Message != "" {
			return e.Message
		}
		return "semgrep reported a scan error"
	}
	return ""
}

// npmAuditRunError returns a non-empty reason when npm audit's top-level `error`
// object is present (the audit failed to run). Returns "" otherwise or when the
// output is not valid JSON.
func npmAuditRunError(out []byte) string {
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Summary string `json:"summary"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return ""
	}
	if doc.Error.Code == "" && doc.Error.Summary == "" {
		return ""
	}
	switch {
	case doc.Error.Code != "" && doc.Error.Summary != "":
		return doc.Error.Code + ": " + doc.Error.Summary
	case doc.Error.Code != "":
		return doc.Error.Code
	default:
		return doc.Error.Summary
	}
}

// govulncheckCompleted reports whether a govulncheck invocation actually ran a
// full analysis. govulncheck exits 0 when it finds no vulnerabilities and 3 when
// it does (both successful runs, per golang.org/x/vuln internal/scan/errors.go).
// Every other outcome — exit 1 (package load / network / config error), exit 2
// (usage), or a non-*exec.ExitError failure such as a context timeout — means it
// never inspected the code and its empty text output must not be read as clean.
func govulncheckCompleted(runErr error) bool {
	if runErr == nil {
		return true // exit 0: analysis completed, no vulnerabilities
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) && ee.ExitCode() == 3 {
		return true // exit 3: analysis completed, vulnerabilities found
	}
	return false
}

// scannerFailureDetail summarizes why a scanner run failed for logging: the
// first non-empty line of tool output (usually the diagnostic), falling back to
// the exec error string.
func scannerFailureDetail(out []byte, runErr error) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line
		}
	}
	if runErr != nil {
		return runErr.Error()
	}
	return "no output"
}
