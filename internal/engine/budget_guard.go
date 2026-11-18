package engine

import (
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
	// Undetermined is set when actual spend could not be computed because the
	// metrics ledger failed to read (a genuine I/O/parse error — not a missing
	// file, which reads as empty). The guard cannot prove the requirement is
	// under budget, so callers must fail safe (pause for review) rather than
	// treat it as $0 spent, which would silently uncap the budget.
	Undetermined bool
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

	mu     sync.Mutex
	warned map[string]bool // reqID → warning already surfaced this run
}

// NewBudgetGuard builds a guard pricing metrics from metricsPath. Returns nil
// when no budget is configured, so callers can wire it unconditionally and a
// nil guard just disables enforcement.
func NewBudgetGuard(billing config.BillingConfig, metricsPath string) *BudgetGuard {
	if billing.BudgetUSD <= 0 {
		return nil
	}
	return &BudgetGuard{
		billing:     billing,
		metricsPath: metricsPath,
		warned:      map[string]bool{},
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
		// A missing ledger is reported by ReadAll as (nil, nil), so reaching
		// here means a real read/parse failure and actual spend is unknown.
		// Reporting $0 would fail open and silently disable the cap, so flag the
		// check as undetermined and let the caller pause for review instead.
		status.Undetermined = true
		return status
	}
	for _, e := range entries {
		if e.ReqID != "" && e.ReqID != reqID {
			continue
		}
		status.SpentUSD += g.costFor(e.Model, e.TokensIn, e.TokensOut)
	}

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

// costFor prices one metrics entry: the model's configured rate when present,
// else the billing default (first configured rate — same fallback the report
// builder uses).
func (g *BudgetGuard) costFor(model string, tokensIn, tokensOut int) float64 {
	if g.billing.LLMCosts.Mode != "per_token" {
		return 0
	}
	if rate, ok := g.billing.LLMCosts.Rates[model]; ok {
		return float64(tokensIn)/1000.0*rate.InputPer1K + float64(tokensOut)/1000.0*rate.OutputPer1K
	}
	return CalculateLLMCost(g.billing, tokensIn, tokensOut)
}
