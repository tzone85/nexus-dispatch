package improver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// writeMetrics helper writes fake metrics.jsonl entries into stateDir.
func writeMetrics(t *testing.T, stateDir string, entries []metrics.MetricEntry) {
	t.Helper()
	rec := metrics.NewRecorder(filepath.Join(stateDir, "metrics.jsonl"))
	for _, e := range entries {
		if err := rec.Record(e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestImprover_NoStateDirReturnsNoSuggestions(t *testing.T) {
	imp := NewImprover()
	got, errs := imp.Run(context.Background(), ProjectInfo{})
	if len(got) != 0 {
		t.Errorf("expected no suggestions, got %d: %+v", len(got), got)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestMetricsAnalyzer_DetectsHighFailureRate(t *testing.T) {
	dir := t.TempDir()
	writeMetrics(t, dir, []metrics.MetricEntry{
		{ReqID: "r1", StoryID: "s1", Phase: "execute", DurationMs: 500, Success: false},
		{ReqID: "r1", StoryID: "s1", Phase: "execute", DurationMs: 600, Success: false},
		{ReqID: "r1", StoryID: "s1", Phase: "execute", DurationMs: 800, Success: false},
		{ReqID: "r1", StoryID: "s2", Phase: "review", DurationMs: 900, Success: true},
	})

	imp := NewImprover()
	out, errs := imp.Run(context.Background(), ProjectInfo{StateDir: dir, Now: time.Now()})
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	hit := false
	for _, s := range out {
		if s.ID == "metrics.high_failure_rate" {
			hit = true
			if s.Severity != SeverityCritical {
				t.Errorf("expected critical severity for 75%% failure, got %s", s.Severity)
			}
			if s.Source != SourceLocal {
				t.Errorf("source = %s, want local", s.Source)
			}
		}
	}
	if !hit {
		t.Errorf("expected high_failure_rate suggestion, got: %+v", out)
	}
}

func TestImprover_OnlineFetcher_AddsSourceOnline(t *testing.T) {
	dir := t.TempDir()
	imp := NewImprover(WithOnline(stubFeed{
		out: []Suggestion{{ID: "online.x", Title: "tip"}},
	}))
	out, errs := imp.Run(context.Background(), ProjectInfo{StateDir: dir, Now: time.Now()})
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(out))
	}
	if out[0].Source != SourceOnline {
		t.Errorf("source = %q, want online", out[0].Source)
	}
}

func TestImprover_OnlineFetcher_ErrorIsReported(t *testing.T) {
	imp := NewImprover(WithOnline(stubFeed{err: errors.New("net down")}))
	_, errs := imp.Run(context.Background(), ProjectInfo{StateDir: t.TempDir()})
	if len(errs) == 0 {
		t.Fatal("expected an error from online fetcher")
	}
}

func TestImprover_RunRespectsCtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	imp := NewImprover(WithAnalyzer(AnalyzerFunc{
		Label: "should_not_run",
		Fn: func(ctx context.Context, _ ProjectInfo) ([]Suggestion, error) {
			t.Error("analyzer ran despite cancellation")
			return nil, nil
		},
	}))
	_, errs := imp.Run(ctx, ProjectInfo{})
	if len(errs) == 0 {
		t.Error("expected ctx error to surface")
	}
}

func TestSeverity_OrdersCriticalFirst(t *testing.T) {
	imp := NewImprover(
		WithAnalyzer(AnalyzerFunc{Label: "static", Fn: func(ctx context.Context, _ ProjectInfo) ([]Suggestion, error) {
			return []Suggestion{
				{ID: "a", Severity: SeverityInfo, Title: "info"},
				{ID: "b", Severity: SeverityCritical, Title: "crit"},
				{ID: "c", Severity: SeverityWarning, Title: "warn"},
			}, nil
		}}),
	)
	// Replace the default analyzer set with just our static one so the
	// order check isn't influenced by the metrics analyzer.
	imp.analyzers = imp.analyzers[len(imp.analyzers)-1:]

	out, _ := imp.Run(context.Background(), ProjectInfo{})
	if len(out) != 3 {
		t.Fatalf("expected 3 suggestions, got %d", len(out))
	}
	if out[0].Severity != SeverityCritical {
		t.Errorf("first severity = %s, want critical", out[0].Severity)
	}
	if out[2].Severity != SeverityInfo {
		t.Errorf("last severity = %s, want info", out[2].Severity)
	}
}

func TestSaveAndLoadSuggestions_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "improvements.json")

	in := []Suggestion{
		{ID: "x", Title: "T", Severity: SeverityWarning, CreatedAt: time.Unix(1, 0).UTC()},
	}
	if err := SaveSuggestions(path, in); err != nil {
		t.Fatalf("SaveSuggestions: %v", err)
	}

	out, err := LoadSuggestions(path)
	if err != nil {
		t.Fatalf("LoadSuggestions: %v", err)
	}
	if len(out) != 1 || out[0].ID != "x" {
		t.Errorf("round-trip mismatch: got %+v", out)
	}
}

func TestLoadSuggestions_MissingFileReturnsNil(t *testing.T) {
	out, err := LoadSuggestions(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil for missing file, got %+v", out)
	}
}

// Tiny diagnostic to keep the JSON format documented in tests; if the
// shape ever drifts, this test fails loudly.
func TestSuggestionJSONShape(t *testing.T) {
	s := Suggestion{
		ID: "foo", Title: "bar", Severity: SeverityCritical,
		Source: SourceLocal, CreatedAt: time.Unix(0, 0).UTC(),
	}
	data, _ := json.Marshal(s)
	want := `"severity":"critical"`
	if !strings.Contains(string(data), want) {
		t.Errorf("want %q in %s", want, data)
	}
}

// stubFeed is a tiny OnlineFetcher used by tests. Returns the
// configured slice / error verbatim.
type stubFeed struct {
	out []Suggestion
	err error
}

func (s stubFeed) Fetch(_ context.Context, _ ProjectInfo) ([]Suggestion, error) {
	return s.out, s.err
}
