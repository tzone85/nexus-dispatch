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
	// Err is set when the usage metrics could not be read, so the reported
	// spend is unreliable (partial or zero). Callers must fail closed on a
	// non-nil Err rather than trust SpentUSD/State — under-counting spend is
	// the one direction this guard must never take (see Check).
	Err error
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

	// A missing metrics file is not an error: ReadAll returns (nil, nil) for it,
	// which correctly prices as $0 spent. A non-nil err is a genuine read
	// failure (permission denied, an over-long JSONL line, etc.). ReadAll still
	// returns whatever records it parsed before the failure, so price those —
	// counting more spend fails safe — but surface the error so the caller can
	// fail closed instead of concluding "under budget" from an unreliable total.
	entries, err := metrics.NewRecorder(g.metricsPath).ReadAll()
	for _, e := range entries {
		if e.ReqID != "" && e.ReqID != reqID {
			continue
		}
		status.SpentUSD += g.costFor(e.Model, e.TokensIn, e.TokensOut)
	}
	if err != nil {
		status.Err = err
		return status
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
