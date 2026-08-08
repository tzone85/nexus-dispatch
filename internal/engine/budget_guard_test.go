package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func budgetBilling(budget, warnPct float64) config.BillingConfig {
	return config.BillingConfig{
		DefaultRate:   150,
		Currency:      "USD",
		BudgetUSD:     budget,
		BudgetWarnPct: warnPct,
		LLMCosts: config.LLMCostConfig{
			Mode: "per_token",
			Rates: map[string]config.TokenRate{
				// $1 per 1k input, $2 per 1k output — easy mental math.
				"m1": {InputPer1K: 1, OutputPer1K: 2},
			},
		},
	}
}

func writeMetrics(t *testing.T, path string, entries ...metrics.MetricEntry) {
	t.Helper()
	rec := metrics.NewRecorder(path)
	for _, e := range entries {
		if err := rec.Record(e); err != nil {
			t.Fatalf("record metric: %v", err)
		}
	}
}

func TestNewBudgetGuard_NilWhenNoBudget(t *testing.T) {
	if g := NewBudgetGuard(config.BillingConfig{}, "unused"); g != nil {
		t.Fatal("no budget configured must return a nil guard (enforcement off)")
	}
}

func TestBudgetGuard_Check(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	// 2000 in ($2) + 1000 out ($2) = $4 for req-a; separate req-b spend must
	// not count toward req-a.
	writeMetrics(t, path,
		metrics.MetricEntry{Timestamp: time.Now(), ReqID: "req-a", Model: "m1", TokensIn: 2000, TokensOut: 1000},
		metrics.MetricEntry{Timestamp: time.Now(), ReqID: "req-b", Model: "m1", TokensIn: 9000, TokensOut: 9000},
	)

	t.Run("under threshold is OK", func(t *testing.T) {
		g := NewBudgetGuard(budgetBilling(10, 80), path)
		st := g.Check("req-a")
		if st.State != BudgetOK {
			t.Errorf("want BudgetOK at $4/$10, got %v", st.State)
		}
		if st.SpentUSD < 3.99 || st.SpentUSD > 4.01 {
			t.Errorf("want spend ≈ $4, got %f", st.SpentUSD)
		}
	})

	t.Run("crossing warn pct warns", func(t *testing.T) {
		g := NewBudgetGuard(budgetBilling(5, 80), path) // warn at $4
		if st := g.Check("req-a"); st.State != BudgetWarn {
			t.Errorf("want BudgetWarn at $4/$5 (80%%), got %v", st.State)
		}
	})

	t.Run("reaching budget exceeds", func(t *testing.T) {
		g := NewBudgetGuard(budgetBilling(4, 80), path)
		if st := g.Check("req-a"); st.State != BudgetExceeded {
			t.Errorf("want BudgetExceeded at $4/$4, got %v", st.State)
		}
	})

	t.Run("missing metrics file spends zero", func(t *testing.T) {
		g := NewBudgetGuard(budgetBilling(4, 80), filepath.Join(dir, "absent.jsonl"))
		if st := g.Check("req-a"); st.State != BudgetOK || st.SpentUSD != 0 {
			t.Errorf("missing metrics must read as $0/OK, got %+v", st)
		}
	})

	t.Run("subscription mode never trips", func(t *testing.T) {
		b := budgetBilling(1, 80)
		b.LLMCosts.Mode = "subscription"
		g := NewBudgetGuard(b, path)
		if st := g.Check("req-a"); st.State != BudgetOK {
			t.Errorf("subscription mode must never trip the guard, got %v", st.State)
		}
	})
}

func TestBudgetGuard_MarkWarnedOnce(t *testing.T) {
	g := NewBudgetGuard(budgetBilling(10, 80), "unused")
	if !g.MarkWarned("r1") {
		t.Fatal("first MarkWarned must return true")
	}
	if g.MarkWarned("r1") {
		t.Fatal("second MarkWarned for the same req must return false")
	}
	if !g.MarkWarned("r2") {
		t.Fatal("a different req warns independently")
	}
}

// TestMonitor_EnforceBudget drives the monitor-level enforcement end to end
// against real stores: an exceeded budget must emit REQ_BUDGET_EXCEEDED and
// pause the requirement; a warning must emit REQ_BUDGET_WARNING exactly once
// and never pause.
func TestMonitor_EnforceBudget(t *testing.T) {
	newEnv := func(t *testing.T, budget float64) (*Monitor, state.EventStore, string) {
		es, ps := capacityTestStores(t)
		seedCapacityStory(t, es, ps, "req-bg", "story-bg")
		path := filepath.Join(t.TempDir(), "metrics.jsonl")
		writeMetrics(t, path,
			metrics.MetricEntry{Timestamp: time.Now(), ReqID: "req-bg", Model: "m1", TokensIn: 2000, TokensOut: 1000}, // $4
		)
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		m.SetBudgetGuard(NewBudgetGuard(budgetBilling(budget, 80), path))
		return m, es, "story-bg"
	}

	t.Run("exceeded pauses and emits", func(t *testing.T) {
		m, es, storyID := newEnv(t, 3) // $4 spent > $3 budget
		if !m.enforceBudget(storyID) {
			t.Fatal("enforceBudget must report exhaustion")
		}
		if evts, _ := es.List(state.EventFilter{Type: state.EventReqBudgetExceeded}); len(evts) != 1 {
			t.Errorf("want one REQ_BUDGET_EXCEEDED, got %d", len(evts))
		}
		if evts, _ := es.List(state.EventFilter{Type: state.EventReqPaused}); len(evts) != 1 {
			t.Errorf("want the requirement paused, got %d pause events", len(evts))
		}
	})

	t.Run("warning emits once and continues", func(t *testing.T) {
		m, es, storyID := newEnv(t, 5) // $4 spent, warn at $4 (80% of $5)
		if m.enforceBudget(storyID) {
			t.Fatal("a warning must not stop the pipeline")
		}
		if m.enforceBudget(storyID) {
			t.Fatal("second check must still not stop the pipeline")
		}
		if evts, _ := es.List(state.EventFilter{Type: state.EventReqBudgetWarning}); len(evts) != 1 {
			t.Errorf("warning must fire exactly once, got %d", len(evts))
		}
		if evts, _ := es.List(state.EventFilter{Type: state.EventReqPaused}); len(evts) != 0 {
			t.Errorf("warning must not pause, got %d pause events", len(evts))
		}
	})

	t.Run("nil guard is a no-op", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		seedCapacityStory(t, es, ps, "req-ng", "story-ng")
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		if m.enforceBudget("story-ng") {
			t.Fatal("nil guard must never stop the pipeline")
		}
	})
}
