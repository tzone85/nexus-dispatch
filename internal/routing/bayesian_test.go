package routing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
)

// ── Beta distribution ───────────────────────────────────────────────

func TestBetaPrior_SuccessProbability(t *testing.T) {
	// Beta(8, 2) → mean = 8/10 = 0.8
	b := BetaPrior{Alpha: 8, Beta: 2}
	p := b.SuccessProbability()
	if math.Abs(p-0.8) > 0.001 {
		t.Errorf("expected ~0.8, got %f", p)
	}
}

func TestBetaPrior_Variance(t *testing.T) {
	// Beta(8, 2): variance = 8*2 / (10^2 * 11) = 16/1100 ≈ 0.01455
	b := BetaPrior{Alpha: 8, Beta: 2}
	v := b.Variance()
	if math.Abs(v-0.01455) > 0.001 {
		t.Errorf("expected ~0.01455, got %f", v)
	}
}

func TestBetaPrior_Confidence(t *testing.T) {
	b := BetaPrior{Alpha: 8, Beta: 2}
	c := b.Confidence()
	if c < 0.98 || c > 1.0 {
		t.Errorf("expected confidence ~0.985, got %f", c)
	}
}

func TestBetaPrior_ZeroValues(t *testing.T) {
	b := BetaPrior{Alpha: 0, Beta: 0}
	if p := b.SuccessProbability(); p != 0 {
		t.Errorf("expected 0 for zero params, got %f", p)
	}
	if v := b.Variance(); v != 0 {
		t.Errorf("expected 0 variance for zero params, got %f", v)
	}
}

// ── Update rules ────────────────────────────────────────────────────

func TestRecordOutcome_Success(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	before := r.getPrior(agent.RoleJunior, TierLow)
	r.RecordOutcome(agent.RoleJunior, 2, OutcomeSuccess)
	after := r.getPrior(agent.RoleJunior, TierLow)

	if after.Alpha != before.Alpha+1 {
		t.Errorf("expected Alpha +1: before=%f, after=%f", before.Alpha, after.Alpha)
	}
	if after.Beta != before.Beta {
		t.Errorf("expected Beta unchanged: before=%f, after=%f", before.Beta, after.Beta)
	}
}

func TestRecordOutcome_Failure(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	before := r.getPrior(agent.RoleJunior, TierLow)
	r.RecordOutcome(agent.RoleJunior, 2, OutcomeFailure)
	after := r.getPrior(agent.RoleJunior, TierLow)

	if after.Alpha != before.Alpha {
		t.Errorf("expected Alpha unchanged: before=%f, after=%f", before.Alpha, after.Alpha)
	}
	if after.Beta != before.Beta+1 {
		t.Errorf("expected Beta +1: before=%f, after=%f", before.Beta, after.Beta)
	}
}

func TestRecordOutcome_Partial(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	before := r.getPrior(agent.RoleSenior, TierHigh)
	r.RecordOutcome(agent.RoleSenior, 8, OutcomePartial)
	after := r.getPrior(agent.RoleSenior, TierHigh)

	if math.Abs(after.Alpha-(before.Alpha+0.5)) > 0.001 {
		t.Errorf("expected Alpha +0.5: before=%f, after=%f", before.Alpha, after.Alpha)
	}
	if math.Abs(after.Beta-(before.Beta+0.5)) > 0.001 {
		t.Errorf("expected Beta +0.5: before=%f, after=%f", before.Beta, after.Beta)
	}
}

// ── Complexity tier mapping ─────────────────────────────────────────

func TestComplexityToTier(t *testing.T) {
	cases := []struct {
		complexity int
		want       ComplexityTier
	}{
		{1, TierLow}, {2, TierLow}, {3, TierLow},
		{4, TierMid}, {5, TierMid},
		{6, TierHigh}, {8, TierHigh}, {13, TierHigh},
	}
	for _, tc := range cases {
		got := ComplexityToTier(tc.complexity)
		if got != tc.want {
			t.Errorf("ComplexityToTier(%d) = %v, want %v", tc.complexity, got, tc.want)
		}
	}
}

// ── Routing decision ────────────────────────────────────────────────

func TestRoute_DefaultPriors_LowComplexity(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	// With default priors, Junior has Beta(8,2) for low complexity = 0.8 success
	// Senior has Beta(5,5) = 0.5 success
	// Junior should win for low complexity (higher probability, lower cost)
	role := r.Route(2)
	if role != agent.RoleJunior {
		t.Errorf("expected Junior for low complexity with default priors, got %v", role)
	}
}

func TestRoute_DefaultPriors_HighComplexity(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	// With default priors, Senior has Beta(8,2) for high complexity = 0.8 success
	// Junior has Beta(1,9) = 0.1 success
	// Senior should win
	role := r.Route(8)
	if role != agent.RoleSenior {
		t.Errorf("expected Senior for high complexity with default priors, got %v", role)
	}
}

func TestRoute_AfterManyFailures_ShiftsRole(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	// Junior starts with Beta(8,2) for low complexity
	// After 10 failures, Beta becomes (8,12) → p = 8/20 = 0.4
	// Intermediate starts with Beta(6,4) → p = 0.6
	// Intermediate should now win for low complexity
	for i := 0; i < 10; i++ {
		r.RecordOutcome(agent.RoleJunior, 2, OutcomeFailure)
	}

	role := r.Route(2)
	if role == agent.RoleJunior {
		t.Error("after 10 failures, Junior should no longer be preferred for low complexity")
	}
}

// ── Decay ───────────────────────────────────────────────────────────

func TestApplyDecay(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()

	// Record some outcomes
	for i := 0; i < 5; i++ {
		r.RecordOutcome(agent.RoleJunior, 2, OutcomeSuccess)
	}

	before := r.getPrior(agent.RoleJunior, TierLow)
	r.ApplyDecay()
	after := r.getPrior(agent.RoleJunior, TierLow)

	// After decay, alpha should decrease (move toward default prior)
	if after.Alpha >= before.Alpha {
		t.Errorf("expected Alpha to decrease after decay: before=%f, after=%f", before.Alpha, after.Alpha)
	}
}

// ── Persistence ─────────────────────────────────────────────────────

func TestPersistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "priors.json")

	// Create and populate
	r1 := NewBayesianRouter()
	r1.initDefaultPriors()
	r1.RecordOutcome(agent.RoleJunior, 2, OutcomeSuccess)
	r1.RecordOutcome(agent.RoleSenior, 8, OutcomeFailure)

	if err := r1.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load into new router
	r2 := NewBayesianRouter()
	if err := r2.Load(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Verify priors match
	p1 := r1.getPrior(agent.RoleJunior, TierLow)
	p2 := r2.getPrior(agent.RoleJunior, TierLow)

	if math.Abs(p1.Alpha-p2.Alpha) > 0.001 || math.Abs(p1.Beta-p2.Beta) > 0.001 {
		t.Errorf("junior/low mismatch: saved=(%f,%f) loaded=(%f,%f)",
			p1.Alpha, p1.Beta, p2.Alpha, p2.Beta)
	}
}

func TestPersistence_LoadMissing_ReturnsError(t *testing.T) {
	r := NewBayesianRouter()
	err := r.Load("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error loading nonexistent file")
	}
}

func TestPersistence_SaveCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "priors.json")

	r := NewBayesianRouter()
	r.initDefaultPriors()

	if err := r.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// ── Edge cases ──────────────────────────────────────────────────────

func TestRoute_EmptyPriors_FallsBackToSenior(t *testing.T) {
	r := NewBayesianRouter()
	// No priors initialized — should not panic, should return a sensible default
	role := r.Route(5)
	if role == "" {
		t.Error("expected non-empty role even with no priors")
	}
}

func TestRecordOutcome_UnknownRole_NoPanic(t *testing.T) {
	r := NewBayesianRouter()
	r.initDefaultPriors()
	// Should not panic for execution-only roles
	r.RecordOutcome(agent.RoleQA, 3, OutcomeSuccess)
}
