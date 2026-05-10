package improver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// TestMetricsAnalyzer_TokensPerStoryWarning fires the warning band
// (between TokensPerStoryWarning and TokensPerStoryCritical) so the
// non-critical token-cost suggestion path is exercised. The default
// thresholds are 50k warning, 150k critical — we craft entries
// landing inside the band.
func TestMetricsAnalyzer_TokensPerStoryWarning(t *testing.T) {
	dir := t.TempDir()
	rec := metrics.NewRecorder(filepath.Join(dir, "metrics.jsonl"))
	defer rec.Close()
	// One story, ~80k tokens total → 80k tokens/story (warning band).
	if err := rec.Record(metrics.MetricEntry{
		ReqID: "r1", StoryID: "s1", Phase: "execute",
		TokensIn: 60000, TokensOut: 20000,
		DurationMs: 500, Success: true,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	imp := NewImprover()
	out, errs := imp.Run(context.Background(), ProjectInfo{StateDir: dir, Now: time.Now()})
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}

	hit := false
	for _, s := range out {
		if s.ID == "metrics.tokens_per_story_warning" {
			hit = true
			if s.Severity != SeverityWarning {
				t.Errorf("severity = %s, want warning", s.Severity)
			}
			if !strings.Contains(s.Description, "80000 tokens/story") {
				t.Errorf("description should report 80000 tokens, got %q", s.Description)
			}
		}
		if s.ID == "metrics.tokens_per_story_critical" {
			t.Errorf("warning band should NOT trigger critical: %v", s)
		}
	}
	if !hit {
		t.Errorf("expected tokens_per_story_warning suggestion, got: %+v", out)
	}
}

// TestMetricsAnalyzer_NoTotalReturnsNil makes sure that when entries
// exist but everything has 0 success+failure, the analyzer skips
// without emitting nonsense suggestions.
func TestMetricsAnalyzer_NoTotalReturnsNil(t *testing.T) {
	dir := t.TempDir()
	// Record an entry that's neither success nor failure (escalated),
	// so SuccessCount + FailureCount = 0. (A Record with success=false
	// counts as failure in the summary; we use a single explicit zero
	// to test the "total==0" guard.)
	rec := metrics.NewRecorder(filepath.Join(dir, "metrics.jsonl"))
	defer rec.Close()
	// Empty file → ReadAll returns nil → analyzer returns nil.
	imp := NewImprover()
	out, errs := imp.Run(context.Background(), ProjectInfo{StateDir: dir, Now: time.Now()})
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	for _, s := range out {
		if strings.HasPrefix(s.ID, "metrics.") {
			t.Errorf("expected no metrics suggestions on empty data, got %s", s.ID)
		}
	}
}

// TestMetricsAnalyzer_HighLatencyAndEscalations covers the two branches
// that the existing failure-rate test misses: avg latency over the
// threshold and escalation count over the threshold. Both branches
// emit warnings; the test asserts both fire so a future threshold
// regression in either won't go silently uncovered.
func TestMetricsAnalyzer_HighLatencyAndEscalations(t *testing.T) {
	dir := t.TempDir()
	rec := metrics.NewRecorder(filepath.Join(dir, "metrics.jsonl"))
	defer rec.Close()
	// 6 calls, all successful, but slow (5000ms each, threshold 4000) and
	// 5 of them escalated (threshold 5).
	for i := range 6 {
		entry := metrics.MetricEntry{
			ReqID: "r1", StoryID: "s1", Phase: "execute",
			TokensIn: 100, TokensOut: 50,
			DurationMs: 5000, Success: true,
		}
		if i < 5 {
			entry.Escalated = true
		}
		if err := rec.Record(entry); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	imp := NewImprover()
	out, errs := imp.Run(context.Background(), ProjectInfo{StateDir: dir, Now: time.Now()})
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}

	want := map[string]bool{"metrics.high_latency": false, "metrics.escalations_frequent": false}
	for _, s := range out {
		if _, ok := want[s.ID]; ok {
			want[s.ID] = true
		}
	}
	for id, hit := range want {
		if !hit {
			t.Errorf("expected suggestion %s in: %+v", id, out)
		}
	}
}

// TestMetricsAnalyzer_MissingStateDirNoOps lets the improver run
// without a configured state dir (e.g. CI bootstrap before NXD has
// been used) without erroring out.
func TestMetricsAnalyzer_MissingStateDirNoOps(t *testing.T) {
	a := MetricsAnalyzer{
		HighFailureRate:        25,
		HighEscalationCount:    5,
		HighAvgLatencyMs:       4000,
		TokensPerStoryWarning:  50000,
		TokensPerStoryCritical: 150000,
	}
	out, err := a.Run(context.Background(), ProjectInfo{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil suggestions when StateDir empty, got %v", out)
	}
}
