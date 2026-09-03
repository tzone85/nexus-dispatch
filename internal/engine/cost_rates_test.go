package engine_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
)

func perTokenBilling(rates map[string]config.TokenRate) config.BillingConfig {
	return config.BillingConfig{
		DefaultRate: 150,
		Currency:    "USD",
		LLMCosts:    config.LLMCostConfig{Mode: "per_token", Rates: rates},
	}
}

func TestRateFor_Resolution(t *testing.T) {
	rates := map[string]config.TokenRate{
		"claude":          {InputPer1K: 1, OutputPer1K: 1},
		"claude-sonnet":   {InputPer1K: 3, OutputPer1K: 15},
		"claude-sonnet-4": {InputPer1K: 4, OutputPer1K: 20},
		"default":         {InputPer1K: 9, OutputPer1K: 9},
	}
	billing := perTokenBilling(rates)
	tests := []struct {
		model   string
		wantIn  float64
		wantHit bool
	}{
		{model: "claude-sonnet-4", wantIn: 4, wantHit: true},          // exact beats prefix
		{model: "claude-sonnet-4-20250514", wantIn: 4, wantHit: true}, // longest prefix
		{model: "claude-sonnet", wantIn: 3, wantHit: true},            // exact
		{model: "claude-opus-4", wantIn: 1, wantHit: true},            // shorter prefix
		{model: "gemma4:26b", wantIn: 9, wantHit: true},               // default key
		{model: "", wantIn: 9, wantHit: true},                         // empty model → default
		{model: "claud", wantIn: 9, wantHit: true},                    // no prefix match → default
		{model: "CLAUDE-sonnet", wantIn: 9, wantHit: true},            // prefix match is case-sensitive
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			rate, ok := engine.RateFor(billing, tt.model)
			if ok != tt.wantHit {
				t.Fatalf("ok = %v, want %v", ok, tt.wantHit)
			}
			if rate.InputPer1K != tt.wantIn {
				t.Fatalf("InputPer1K = %v, want %v", rate.InputPer1K, tt.wantIn)
			}
		})
	}
}

func TestRateFor_UnpricedWithoutDefault(t *testing.T) {
	billing := perTokenBilling(map[string]config.TokenRate{"gpt-4o": {InputPer1K: 5, OutputPer1K: 15}})
	if rate, ok := engine.RateFor(billing, "gemma4"); ok || rate != (config.TokenRate{}) {
		t.Fatalf("unpriced model must report ok=false and zero rate, got %+v %v", rate, ok)
	}
	if _, ok := engine.RateFor(config.BillingConfig{}, "x"); ok {
		t.Fatal("no rates configured must be unpriced")
	}
}

func TestCalculateLLMCost_DeterministicAcrossRuns(t *testing.T) {
	// Two rates that both prefix-match; a map range would pick either at random.
	billing := perTokenBilling(map[string]config.TokenRate{
		"claude":        {InputPer1K: 1, OutputPer1K: 1},
		"claude-sonnet": {InputPer1K: 3, OutputPer1K: 15},
	})
	want, ok := engine.CalculateLLMCost(billing, "claude-sonnet-4", 1000, 500)
	if !ok {
		t.Fatal("expected priced model")
	}
	if want < 10.49 || want > 10.51 {
		t.Fatalf("expected ~$10.50 from the longest-prefix rate, got %f", want)
	}
	for i := 0; i < 100; i++ {
		got, ok := engine.CalculateLLMCost(billing, "claude-sonnet-4", 1000, 500)
		if !ok || got != want {
			t.Fatalf("run %d: got %f (ok=%v), want %f — nondeterministic rate selection", i, got, ok, want)
		}
	}
}

func TestCalculateLLMCost_Modes(t *testing.T) {
	priced := perTokenBilling(map[string]config.TokenRate{"m": {InputPer1K: 3, OutputPer1K: 15}})

	if c, ok := engine.CalculateLLMCost(priced, "m", 1000, 500); !ok || c < 10.49 || c > 10.51 {
		t.Errorf("priced: got %f ok=%v", c, ok)
	}
	if c, ok := engine.CalculateLLMCost(priced, "other", 1000, 500); ok || c != 0 {
		t.Errorf("unpriced model must return 0,false; got %f ok=%v", c, ok)
	}
	sub := config.BillingConfig{LLMCosts: config.LLMCostConfig{Mode: "subscription"}}
	if c, ok := engine.CalculateLLMCost(sub, "anything", 10000, 5000); !ok || c != 0 {
		t.Errorf("subscription is priced at $0: got %f ok=%v", c, ok)
	}
	if c, ok := engine.CalculateLLMCost(perTokenBilling(nil), "m", 1000, 500); ok || c != 0 {
		t.Errorf("no rates: got %f ok=%v", c, ok)
	}
}

func TestCalculateCostWithTokens_UsesModelRate(t *testing.T) {
	billing := perTokenBilling(map[string]config.TokenRate{
		"cheap":     {InputPer1K: 0.001, OutputPer1K: 0.001},
		"expensive": {InputPer1K: 3, OutputPer1K: 15},
	})
	billing.HoursPerPoint = map[int][2]float64{3: {2, 4}}
	stories := []engine.StoryEstimate{{Title: "a", Complexity: 3}}

	est := engine.CalculateCostWithTokens(stories, billing, 0, "expensive", 1000, 500)
	if est.Summary.LLMCost < 10.49 || est.Summary.LLMCost > 10.51 {
		t.Fatalf("LLMCost = %f, want ~10.50", est.Summary.LLMCost)
	}
	// QuoteHigh = 4h * $150 = $600 → margin = (1 - 10.5/600) * 100 = 98.25
	if est.Summary.MarginPercent < 98.2 || est.Summary.MarginPercent > 98.3 {
		t.Fatalf("MarginPercent = %f", est.Summary.MarginPercent)
	}

	unpriced := engine.CalculateCostWithTokens(stories, billing, 0, "mystery", 1000, 500)
	if unpriced.Summary.LLMCost != 0 || unpriced.Summary.MarginPercent != 100 {
		t.Fatalf("unpriced model must not invent cost: %+v", unpriced.Summary)
	}
}

func TestApplyLLMSpend(t *testing.T) {
	billing := perTokenBilling(nil)
	billing.HoursPerPoint = map[int][2]float64{3: {2, 4}}
	est := engine.CalculateCost([]engine.StoryEstimate{{Complexity: 3}}, billing, 0)
	got := engine.ApplyLLMSpend(est, 60)
	if got.Summary.LLMCost != 60 || got.Summary.MarginPercent != 90 {
		t.Fatalf("ApplyLLMSpend: %+v", got.Summary)
	}
	if est.Summary.LLMCost != 0 {
		t.Fatal("ApplyLLMSpend must not mutate its input")
	}
	if z := engine.ApplyLLMSpend(est, 0); z.Summary.MarginPercent != 100 {
		t.Fatalf("zero spend keeps 100%% margin, got %+v", z.Summary)
	}
}
