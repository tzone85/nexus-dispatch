package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/security"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestSecurityScanCmd(t *testing.T) {
	t.Run("markdown report on a clean repo", func(t *testing.T) {
		env := setupTestEnv(t)
		repo := t.TempDir()
		initTestRepo(t, repo)

		out, err := execCmd(t, newSecurityScanCmd(), env.Config, repo)
		if err != nil {
			t.Fatalf("scan: %v\n%s", err, out)
		}
		for _, want := range []string{
			"## Security scan — " + repo,
			"Findings: 0 total — 0 critical, 0 high, 0 medium, 0 low, 0 info",
			"KB v1",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		// The scan is auditable: SECURITY_SCAN_COMPLETED landed in the event log.
		if n, _ := env.Events.Count(state.EventFilter{Type: state.EventSecurityScanCompleted}); n != 1 {
			t.Errorf("SECURITY_SCAN_COMPLETED events = %d, want 1", n)
		}
	})

	t.Run("json report", func(t *testing.T) {
		env := setupTestEnv(t)
		repo := t.TempDir()
		initTestRepo(t, repo)

		out, err := execCmd(t, newSecurityScanCmd(), env.Config, repo, "--json", "--min", "low")
		if err != nil {
			t.Fatalf("scan --json: %v\n%s", err, out)
		}
		var report security.Report
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("output is not a JSON report: %v\n%s", err, out)
		}
		if report.RepoDir != repo || report.Total() != 0 || report.KBVersion != 1 {
			t.Errorf("report = %+v", report)
		}
	})

	t.Run("defaults to the working directory", func(t *testing.T) {
		env := setupTestEnv(t)
		repo := t.TempDir()
		initTestRepo(t, repo)
		orig, _ := os.Getwd()
		if err := os.Chdir(repo); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(orig) })

		out, err := execCmd(t, newSecurityScanCmd(), env.Config)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		if !strings.Contains(out, "## Security scan — "+resolved) && !strings.Contains(out, "## Security scan — "+repo) {
			t.Errorf("report should name the cwd repo:\n%s", out)
		}
	})

	t.Run("missing config falls back to defaults", func(t *testing.T) {
		repo := t.TempDir()
		initTestRepo(t, repo)
		home := t.TempDir()
		t.Setenv("HOME", home) // default state_dir is ~/.nxd
		if err := os.MkdirAll(filepath.Join(home, ".nxd"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := execCmd(t, newSecurityScanCmd(), filepath.Join(t.TempDir(), "absent.yaml"), repo)
		if err != nil {
			t.Fatalf("scan without config: %v\n%s", err, out)
		}
		if !strings.Contains(out, "## Security scan — "+repo) {
			t.Errorf("output = %s", out)
		}
	})
}

func TestSecurityKBCmd(t *testing.T) {
	t.Run("baseline listing", func(t *testing.T) {
		env := setupTestEnv(t)
		out, err := execCmd(t, newSecurityKBCmd(), env.Config)
		if err != nil {
			t.Fatalf("kb: %v", err)
		}
		baseline := security.BaselineKnowledgeBase()
		header := "Security knowledge base v1 — " + itoa(len(baseline.Rules)) + " rules (" + itoa(len(baseline.Rules)) + " baseline, 0 learned)"
		if !strings.Contains(out, header) {
			t.Errorf("output lacks %q:\n%s", header, out)
		}
		if strings.Contains(out, " + [") {
			t.Error("baseline rules must not carry the learned marker")
		}
	})

	t.Run("learned rules are marked", func(t *testing.T) {
		env := setupTestEnv(t)
		kbPath := filepath.Join(env.Dir, ".nxd", "security", "knowledge.json")
		kb := security.BaselineKnowledgeBase().Add(security.VulnRule{
			ID: "CWE-9999", Title: "Learned thing", Severity: security.SeverityHigh, Source: security.RuleLearned,
		})
		if err := kb.Save(kbPath); err != nil {
			t.Fatal(err)
		}
		out, err := execCmd(t, newSecurityKBCmd(), env.Config)
		if err != nil {
			t.Fatalf("kb: %v", err)
		}
		if !strings.Contains(out, "1 learned)") || !strings.Contains(out, " + [CWE-9999] Learned thing — high") {
			t.Errorf("output = %s", out)
		}
		if !strings.Contains(out, "Security knowledge base v2") {
			t.Errorf("Add must bump the version:\n%s", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		env := setupTestEnv(t)
		out, err := execCmd(t, newSecurityKBCmd(), env.Config, "--json")
		if err != nil {
			t.Fatalf("kb --json: %v", err)
		}
		var kb security.KnowledgeBase
		if err := json.Unmarshal([]byte(out), &kb); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if kb.Version != 1 || len(kb.Rules) == 0 {
			t.Errorf("kb = version %d, %d rules", kb.Version, len(kb.Rules))
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
