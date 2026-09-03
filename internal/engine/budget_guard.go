package engine

import (
	"log"
	"sort"
	"sync"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// BudgetState classifies a requirement's LLM spend against its budget.
type BudgetState int

const (
	// BudgetOK — under the warning threshold (or no budget configured).
	BudgetOK BudgetState = iota
	// BudgetWarn — spend crossed billing.budget_warn_pct of the budget.
	BudgetWarn
	// BudgetExceeded — spend reached billing.budget_usd; stop spending.
	BudgetExceeded
)

// BudgetStatus is one budget check's outcome.
type BudgetStatus struct {
	State     BudgetState
	SpentUSD  float64
	BudgetUSD float64
	WarnUSD   float64
	// Unpriced lists models that appeared in the metrics but have no rate
	// (exact, prefix or "default") — their spend is NOT in SpentUSD. Sorted.
	Unpriced []string
}

// BudgetGuard enforces billing.budget_usd: it prices the requirement's actual
// token usage (metrics.jsonl) with billing.llm_costs.rates and reports when
// spend crosses the warning threshold or the cap. The guard itself is pure
// bookkeeping — the Monitor decides what to do (emit events, pause).
//
// Only meaningful in per_token mode; in subscription mode spend is always $0
// and the guard never trips.
type BudgetGuard struct {
	billing     config.BillingConfig
	metricsPath string

	mu             sync.Mutex
	warned         map[string]bool // reqID → warning already surfaced this run
	unpricedLogged map[string]bool // model → "unpriced" already logged this run
}

// NewBudgetGuard builds a guard pricing metrics from metricsPath. Returns nil
// when no budget is configured, so callers can wire it unconditionally and a
// nil guard just disables enforcement.
func NewBudgetGuard(billing config.BillingConfig, metricsPath string) *BudgetGuard {
	if billing.BudgetUSD <= 0 {
		return nil
	}
	return &BudgetGuard{
		billing:        billing,
		metricsPath:    metricsPath,
		warned:         map[string]bool{},
		unpricedLogged: map[string]bool{},
	}
}

// warnPct returns the effective warning threshold percentage (default 80).
func (g *BudgetGuard) warnPct() float64 {
	if g.billing.BudgetWarnPct > 0 {
		return g.billing.BudgetWarnPct
	}
	return 80
}

// Check prices the requirement's recorded token usage and classifies it
// against the budget. Metrics with an empty ReqID (older records) are counted
// too — over-counting fails safe (pauses early), under-counting would not.
func (g *BudgetGuard) Check(reqID string) BudgetStatus {
	status := BudgetStatus{
		BudgetUSD: g.billing.BudgetUSD,
		WarnUSD:   g.billing.BudgetUSD * g.warnPct() / 100,
	}

	entries, err := metrics.NewRecorder(g.metricsPath).ReadAll()
	if err != nil {
		return status // no metrics yet — nothing spent
	}
	unpriced := map[string]bool{}
	for _, e := range entries {
		if e.ReqID != "" && e.ReqID != reqID {
			continue
		}
		cost, ok := CalculateLLMCost(g.billing, e.Model, e.TokensIn, e.TokensOut)
		if !ok {
			unpriced[e.Model] = true
			continue
		}
		status.SpentUSD += cost
	}
	status.Unpriced = g.reportUnpriced(unpriced)

	switch {
	case status.SpentUSD >= status.BudgetUSD:
		status.State = BudgetExceeded
	case status.SpentUSD >= status.WarnUSD:
		status.State = BudgetWarn
	}
	return status
}

// MarkWarned records that the warning for reqID has been surfaced and reports
// whether this call was the first (i.e. the caller should emit the event).
func (g *BudgetGuard) MarkWarned(reqID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.warned[reqID] {
		return false
	}
	g.warned[reqID] = true
	return true
}

// reportUnpriced logs each unpriced model once per run and returns the sorted
// list for the status.
func (g *BudgetGuard) reportUnpriced(models map[string]bool) []string {
	if len(models) == 0 {
		return nil
	}
	out := make([]string, 0, len(models))
	for m := range models {
		out = append(out, m)
	}
	sort.Strings(out)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range out {
		if !g.unpricedLogged[m] {
			g.unpricedLogged[m] = true
			log.Printf("[budget] unpriced model %q: no billing.llm_costs.rates entry (exact, prefix or \"default\") — its spend is not counted", m)
		}
	}
	return out
}
