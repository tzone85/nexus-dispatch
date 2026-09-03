package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func writeReportMetrics(t *testing.T, path string, entries ...metrics.MetricEntry) {
	t.Helper()
	rec := metrics.NewRecorder(path)
	for _, e := range entries {
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now()
		}
		if err := rec.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
}

func newUsageBuilder(t *testing.T, cfg config.Config) *ReportBuilder {
	t.Helper()
	es, err := state.NewFileStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { es.Close(); ps.Close() })
	return NewReportBuilder(es, ps, cfg)
}

func TestSumTokenUsage_FiltersByRequirement(t *testing.T) {
	dir := t.TempDir()
	writeReportMetrics(t, filepath.Join(dir, "metrics.jsonl"),
		metrics.MetricEntry{ReqID: "r1", Model: "priced", TokensIn: 1000, TokensOut: 500},
		metrics.MetricEntry{ReqID: "", Model: "priced", TokensIn: 100, TokensOut: 100}, // legacy: counted
		metrics.MetricEntry{ReqID: "r2", Model: "priced", TokensIn: 9000, TokensOut: 9000},
		metrics.MetricEntry{ReqID: "r1", Model: "mystery", TokensIn: 5000, TokensOut: 5000},
	)
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = dir
	cfg.Billing.LLMCosts.Mode = "per_token"
	cfg.Billing.LLMCosts.Rates = map[string]config.TokenRate{"priced": {InputPer1K: 1, OutputPer1K: 2}}

	usage := newUsageBuilder(t, cfg).sumTokenUsage("r1")
	if usage.TokensIn != 6100 || usage.TokensOut != 5600 {
		t.Errorf("tokens = %d/%d, want 6100/5600 (r2 excluded)", usage.TokensIn, usage.TokensOut)
	}
	// priced: (1000+100)/1000*1 + (500+100)/1000*2 = 1.1 + 1.2 = 2.3
	if usage.CostUSD < 2.299 || usage.CostUSD > 2.301 {
		t.Errorf("cost = %f, want 2.30", usage.CostUSD)
	}
	if len(usage.Unpriced) != 1 || usage.Unpriced[0] != "mystery" {
		t.Errorf("unpriced = %v, want [mystery]", usage.Unpriced)
	}
}

func TestBuildEffort_UsesRequirementSpend(t *testing.T) {
	dir := t.TempDir()
	writeReportMetrics(t, filepath.Join(dir, "metrics.jsonl"),
		metrics.MetricEntry{ReqID: "r1", Model: "m", TokensIn: 1000, TokensOut: 1000},
		metrics.MetricEntry{ReqID: "r2", Model: "m", TokensIn: 100000, TokensOut: 100000},
		metrics.MetricEntry{ReqID: "r1", Model: "unknown", TokensIn: 100000, TokensOut: 100000},
	)
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = dir
	cfg.Billing.LLMCosts.Mode = "per_token"
	cfg.Billing.LLMCosts.Rates = map[string]config.TokenRate{"m": {InputPer1K: 1, OutputPer1K: 1}}

	est := newUsageBuilder(t, cfg).buildEffort("r1", []state.Story{{Title: "s", Complexity: 3}})
	if est.Summary.LLMCost != 2 {
		t.Errorf("LLMCost = %f, want 2.00 (only r1, only priced models)", est.Summary.LLMCost)
	}
	if est.Summary.MarginPercent >= 100 || est.Summary.MarginPercent <= 0 {
		t.Errorf("margin should reflect spend, got %f", est.Summary.MarginPercent)
	}
}

func TestReadMetricEntries_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".nxd-test"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReportMetrics(t, filepath.Join(home, ".nxd-test", "metrics.jsonl"),
		metrics.MetricEntry{ReqID: "r1", Model: "m", TokensIn: 7, TokensOut: 3},
	)
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = "~/.nxd-test"
	usage := newUsageBuilder(t, cfg).sumTokenUsage("r1")
	if usage.TokensIn != 7 || usage.TokensOut != 3 {
		t.Fatalf("~ was not expanded: %+v", usage)
	}
}

func TestExpandHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := map[string]string{
		"~":           home,
		"~/":          home,
		"~/.nxd":      filepath.Join(home, ".nxd"),
		"/abs/path":   "/abs/path",
		"relative":    "relative",
		"~user/.nxd":  "~user/.nxd", // only the current user's ~ is expanded
		"":            "",
		"~/a/../b/.x": filepath.Join(home, "b", ".x"),
	}
	for in, want := range tests {
		if got := expandHomeDir(in); got != want {
			t.Errorf("expandHomeDir(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasPrefix(expandHomeDir("~/x"), home) {
		t.Error("expanded path must live under HOME")
	}
}

func TestExpandHomeDir_NoHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := expandHomeDir("~/.nxd"); got != "~/.nxd" {
		t.Errorf("without a home dir the path must be returned unchanged, got %q", got)
	}
}
