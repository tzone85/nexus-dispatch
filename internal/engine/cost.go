package engine

import (
	"sort"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// Estimate is the full result of a cost estimation.
type Estimate struct {
	EstimateID  string          `json:"estimate_id"`
	Requirement string          `json:"requirement"`
	Project     string          `json:"project"`
	IsQuick     bool            `json:"is_quick"`
	Stories     []StoryEstimate `json:"stories"`
	Summary     EstimateSummary `json:"summary"`
}

// EstimateSummary holds aggregated cost/hours data.
type EstimateSummary struct {
	StoryCount    int     `json:"story_count"`
	TotalPoints   int     `json:"total_points"`
	HoursLow      float64 `json:"hours_low"`
	HoursHigh     float64 `json:"hours_high"`
	QuoteLow      float64 `json:"quote_low"`
	QuoteHigh     float64 `json:"quote_high"`
	LLMCost       float64 `json:"llm_cost"`
	MarginPercent float64 `json:"margin_percent"`
	Rate          float64 `json:"rate"`
	Currency      string  `json:"currency"`
}

// StoryEstimate wraps a planned story with cost projections.
type StoryEstimate struct {
	Title      string  `json:"title"`
	Complexity int     `json:"complexity"`
	Role       string  `json:"role"`
	HoursLow   float64 `json:"hours_low"`
	HoursHigh  float64 `json:"hours_high"`
	CostLow    float64 `json:"cost_low"`
	CostHigh   float64 `json:"cost_high"`
}

// CalculateCost maps stories to hours and cost using billing config.
// If rateOverride > 0, it overrides billing.DefaultRate.
// Returns a new Estimate — no mutation of input stories.
func CalculateCost(stories []StoryEstimate, billing config.BillingConfig, rateOverride float64) Estimate {
	rate := billing.DefaultRate
	if rateOverride > 0 {
		rate = rateOverride
	}

	sortedKeys := sortedFibKeys(billing.HoursPerPoint)

	var totalPoints int
	var totalHoursLow, totalHoursHigh float64

	populated := make([]StoryEstimate, len(stories))
	for i, s := range stories {
		hrs := lookupHours(s.Complexity, billing.HoursPerPoint, sortedKeys)
		populated[i] = StoryEstimate{
			Title:      s.Title,
			Complexity: s.Complexity,
			Role:       s.Role,
			HoursLow:   hrs[0],
			HoursHigh:  hrs[1],
			CostLow:    hrs[0] * rate,
			CostHigh:   hrs[1] * rate,
		}
		totalPoints += s.Complexity
		totalHoursLow += hrs[0]
		totalHoursHigh += hrs[1]
	}

	// A pre-run estimate has no token usage yet: LLM cost is $0 and the
	// margin is 100%. ApplyLLMSpend / CalculateCostWithTokens fold in actuals.
	return Estimate{
		Stories: populated,
		Summary: EstimateSummary{
			StoryCount:    len(stories),
			TotalPoints:   totalPoints,
			HoursLow:      totalHoursLow,
			HoursHigh:     totalHoursHigh,
			QuoteLow:      totalHoursLow * rate,
			QuoteHigh:     totalHoursHigh * rate,
			LLMCost:       0,
			MarginPercent: 100.0,
			Rate:          rate,
			Currency:      billing.Currency,
		},
	}
}

// lookupHours finds the hours range for a given complexity score.
// If no exact match, falls back to the nearest lower Fibonacci key.
// If nothing matches, returns a safe default of [1.0, 2.0].
func lookupHours(complexity int, hoursMap map[int][2]float64, sortedKeys []int) [2]float64 {
	if hrs, ok := hoursMap[complexity]; ok {
		return hrs
	}
	var best int
	for _, k := range sortedKeys {
		if k <= complexity {
			best = k
		}
	}
	if hrs, ok := hoursMap[best]; ok {
		return hrs
	}
	return [2]float64{1.0, 2.0}
}

// sortedFibKeys returns the keys of hoursMap sorted ascending.
func sortedFibKeys(hoursMap map[int][2]float64) []int {
	keys := make([]int, 0, len(hoursMap))
	for k := range hoursMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// defaultRateKey is the billing.llm_costs.rates key that prices any model
// without an exact or prefix match.
const defaultRateKey = "default"

// RateFor resolves the per-token rate for model deterministically:
//  1. exact key match,
//  2. the LONGEST configured key that is a prefix of model (so a rate for
//     "claude-sonnet" prices "claude-sonnet-4-20250514"; the "default" key is
//     never used as a prefix),
//  3. the "default" key when present.
//
// ok is false when the model is unpriced so callers can log it instead of
// silently billing $0 (or a random rate — the previous map-range behaviour).
func RateFor(billing config.BillingConfig, model string) (config.TokenRate, bool) {
	rates := billing.LLMCosts.Rates
	if len(rates) == 0 {
		return config.TokenRate{}, false
	}
	if rate, ok := rates[model]; ok && model != "" {
		return rate, true
	}
	bestKey := ""
	for key := range rates {
		if key == defaultRateKey || key == "" || !strings.HasPrefix(model, key) {
			continue
		}
		if len(key) > len(bestKey) || (len(key) == len(bestKey) && key < bestKey) {
			bestKey = key
		}
	}
	if bestKey != "" {
		return rates[bestKey], true
	}
	if rate, ok := rates[defaultRateKey]; ok {
		return rate, true
	}
	return config.TokenRate{}, false
}

// CalculateLLMCost prices token usage for model. Subscription mode (anything
// other than "per_token") is legitimately $0 with ok=true. In per_token mode
// the rate is resolved with RateFor; an unpriced model yields (0, false) so
// the budget guard and report can surface "unpriced model" rather than
// under-count spend silently.
func CalculateLLMCost(billing config.BillingConfig, model string, inputTokens, outputTokens int) (float64, bool) {
	if billing.LLMCosts.Mode != "per_token" {
		return 0.0, true
	}
	rate, ok := RateFor(billing, model)
	if !ok {
		return 0.0, false
	}
	inputCost := float64(inputTokens) / 1000.0 * rate.InputPer1K
	outputCost := float64(outputTokens) / 1000.0 * rate.OutputPer1K
	return inputCost + outputCost, true
}

// ApplyLLMSpend returns a copy of est with the given actual LLM spend folded
// into the summary (LLMCost + MarginPercent against QuoteHigh).
func ApplyLLMSpend(est Estimate, llmCost float64) Estimate {
	est.Summary.LLMCost = llmCost
	est.Summary.MarginPercent = 100.0
	if llmCost > 0 && est.Summary.QuoteHigh > 0 {
		est.Summary.MarginPercent = (1 - llmCost/est.Summary.QuoteHigh) * 100
	}
	return est
}

// CalculateCostWithTokens is like CalculateCost but also incorporates actual
// LLM token usage for a single model into the cost summary. Use this for
// post-completion estimates where real token counts are available. An
// unpriced model contributes $0 (see CalculateLLMCost).
func CalculateCostWithTokens(stories []StoryEstimate, billing config.BillingConfig, rateOverride float64, model string, inputTokens, outputTokens int) Estimate {
	est := CalculateCost(stories, billing, rateOverride)
	llmCost, _ := CalculateLLMCost(billing, model, inputTokens, outputTokens)
	return ApplyLLMSpend(est, llmCost)
}
