package engine

import (
	"sort"

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

	llmCost := 0.0
	marginPercent := 100.0
	if billing.LLMCosts.Mode == "per_token" && totalHoursHigh*rate > 0 && llmCost > 0 {
		marginPercent = (1 - llmCost/(totalHoursHigh*rate)) * 100
	}

	return Estimate{
		Stories: populated,
		Summary: EstimateSummary{
			StoryCount:    len(stories),
			TotalPoints:   totalPoints,
			HoursLow:      totalHoursLow,
			HoursHigh:     totalHoursHigh,
			QuoteLow:      totalHoursLow * rate,
			QuoteHigh:     totalHoursHigh * rate,
			LLMCost:       llmCost,
			MarginPercent: marginPercent,
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
