// Package routing provides adaptive agent routing based on Bayesian
// inference. It maintains Beta distribution priors per role per complexity
// tier, updating them after each story outcome to improve routing accuracy.
package routing

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/tzone85/nexus-dispatch/internal/agent"
)

// ComplexityTier groups Fibonacci complexity points into three tiers
// for tractable Beta distribution tracking.
type ComplexityTier string

const (
	TierLow  ComplexityTier = "low"  // complexity 1-3
	TierMid  ComplexityTier = "mid"  // complexity 4-5
	TierHigh ComplexityTier = "high" // complexity 6+
)

// ComplexityToTier maps a Fibonacci complexity score to a tier.
func ComplexityToTier(complexity int) ComplexityTier {
	switch {
	case complexity <= 3:
		return TierLow
	case complexity <= 5:
		return TierMid
	default:
		return TierHigh
	}
}

// Outcome represents the result of a story execution.
type Outcome int

const (
	OutcomeSuccess Outcome = iota // QA passed without escalation
	OutcomeFailure                // escalated to higher tier
	OutcomePartial                // passed after retry at same tier
)

// BetaPrior holds the parameters of a Beta distribution.
type BetaPrior struct {
	Alpha float64 `json:"alpha"`
	Beta  float64 `json:"beta"`
}

// SuccessProbability returns the mean of the Beta distribution: α / (α + β).
func (b BetaPrior) SuccessProbability() float64 {
	sum := b.Alpha + b.Beta
	if sum == 0 {
		return 0
	}
	return b.Alpha / sum
}

// Variance returns the variance of the Beta distribution.
func (b BetaPrior) Variance() float64 {
	sum := b.Alpha + b.Beta
	if sum == 0 || sum+1 == 0 {
		return 0
	}
	return (b.Alpha * b.Beta) / (sum * sum * (sum + 1))
}

// Confidence returns 1 - variance, clamped to [0, 1].
func (b BetaPrior) Confidence() float64 {
	c := 1.0 - b.Variance()
	return math.Max(0, math.Min(1, c))
}

// priorKey uniquely identifies a prior by role and complexity tier.
type priorKey struct {
	Role agent.Role
	Tier ComplexityTier
}

// priorKeyJSON is the JSON-serializable form of priorKey.
type priorKeyJSON struct {
	Role string        `json:"role"`
	Tier ComplexityTier `json:"tier"`
}

// priorEntry is a single persisted prior with its key.
type priorEntry struct {
	Key   priorKeyJSON `json:"key"`
	Prior BetaPrior    `json:"prior"`
}

// decayLambda is the per-story exponential decay factor.
// After ~20 stories, early observations contribute < 36% weight.
const decayLambda = 0.95

// costFactor penalizes more expensive roles when success probabilities are close.
var costFactor = map[agent.Role]float64{
	agent.RoleJunior:       0.0, // cheapest
	agent.RoleIntermediate: 0.3,
	agent.RoleSenior:       0.6, // most expensive
}

// executionRoles are the roles eligible for story routing.
var executionRoles = []agent.Role{
	agent.RoleJunior,
	agent.RoleIntermediate,
	agent.RoleSenior,
}

// BayesianRouter maintains Beta priors and routes stories adaptively.
type BayesianRouter struct {
	mu     sync.RWMutex
	priors map[priorKey]BetaPrior
}

// NewBayesianRouter creates a router with no priors loaded.
// Call initDefaultPriors() or Load() before routing.
func NewBayesianRouter() *BayesianRouter {
	return &BayesianRouter{
		priors: make(map[priorKey]BetaPrior),
	}
}

// initDefaultPriors sets uninformative Beta priors for all execution roles.
// These encode the common-sense expectation that juniors handle simple work
// and seniors handle complex work.
func (r *BayesianRouter) initDefaultPriors() {
	defaults := map[priorKey]BetaPrior{
		// Junior: strong at low, weak at mid, very weak at high
		{agent.RoleJunior, TierLow}:  {Alpha: 8, Beta: 2},
		{agent.RoleJunior, TierMid}:  {Alpha: 3, Beta: 7},
		{agent.RoleJunior, TierHigh}: {Alpha: 1, Beta: 9},

		// Intermediate: moderate across the board, strongest at mid
		{agent.RoleIntermediate, TierLow}:  {Alpha: 6, Beta: 4},
		{agent.RoleIntermediate, TierMid}:  {Alpha: 7, Beta: 3},
		{agent.RoleIntermediate, TierHigh}: {Alpha: 3, Beta: 7},

		// Senior: moderate at low, good at mid, strong at high
		{agent.RoleSenior, TierLow}:  {Alpha: 5, Beta: 5},
		{agent.RoleSenior, TierMid}:  {Alpha: 6, Beta: 4},
		{agent.RoleSenior, TierHigh}: {Alpha: 8, Beta: 2},
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range defaults {
		r.priors[k] = v
	}
}

// InitDefaults sets default priors if no priors are loaded yet.
// Safe to call multiple times — only initializes if empty.
func (r *BayesianRouter) InitDefaults() {
	r.mu.RLock()
	empty := len(r.priors) == 0
	r.mu.RUnlock()
	if empty {
		r.initDefaultPriors()
	}
}

// getPrior returns a copy of the prior for the given role and tier.
// Returns a zero BetaPrior if not found.
func (r *BayesianRouter) getPrior(role agent.Role, tier ComplexityTier) BetaPrior {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.priors[priorKey{role, tier}]
}

// RecordOutcome updates the Beta prior for the given role and complexity
// based on the observed outcome.
func (r *BayesianRouter) RecordOutcome(role agent.Role, complexity int, outcome Outcome) {
	tier := ComplexityToTier(complexity)
	key := priorKey{role, tier}

	r.mu.Lock()
	defer r.mu.Unlock()

	prior := r.priors[key]

	switch outcome {
	case OutcomeSuccess:
		prior.Alpha += 1.0
	case OutcomeFailure:
		prior.Beta += 1.0
	case OutcomePartial:
		prior.Alpha += 0.5
		prior.Beta += 0.5
	}

	r.priors[key] = prior
}

// Route selects the best execution role for a story of the given complexity.
// It scores each role by: P(success) × confidence × (1 - costWeight × costFactor)
// and returns the role with the highest score.
//
// Falls back to Senior if no priors are available.
func (r *BayesianRouter) Route(complexity int) agent.Role {
	tier := ComplexityToTier(complexity)

	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.priors) == 0 {
		return agent.RoleSenior
	}

	const costWeight = 0.15 // how much cost influences the decision

	var bestRole agent.Role
	bestScore := -1.0

	for _, role := range executionRoles {
		prior, ok := r.priors[priorKey{role, tier}]
		if !ok {
			continue
		}

		p := prior.SuccessProbability()
		conf := prior.Confidence()
		cf := costFactor[role]

		score := p * conf * (1.0 - costWeight*cf)

		if score > bestScore {
			bestScore = score
			bestRole = role
		}
	}

	if bestRole == "" {
		return agent.RoleSenior
	}
	return bestRole
}

// ApplyDecay decays all priors toward their initial values using exponential
// decay. This prevents early observations from dominating indefinitely.
// Call this periodically (e.g., after each requirement completes).
func (r *BayesianRouter) ApplyDecay() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for key, prior := range r.priors {
		// Decay toward 1.0 (neutral prior). The further alpha/beta are from
		// the baseline, the more decay pulls them back.
		prior.Alpha = 1.0 + (prior.Alpha-1.0)*decayLambda
		prior.Beta = 1.0 + (prior.Beta-1.0)*decayLambda
		r.priors[key] = prior
	}
}

// Save persists all priors to a JSON file at the given path.
// Creates parent directories if needed.
func (r *BayesianRouter) Save(path string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]priorEntry, 0, len(r.priors))
	for k, v := range r.priors {
		entries = append(entries, priorEntry{
			Key:   priorKeyJSON{Role: string(k.Role), Tier: k.Tier},
			Prior: v,
		})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal priors: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create priors directory: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

// Load reads priors from a JSON file at the given path.
// Returns an error if the file doesn't exist or is malformed.
func (r *BayesianRouter) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read priors: %w", err)
	}

	var entries []priorEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("unmarshal priors: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.priors = make(map[priorKey]BetaPrior, len(entries))
	for _, e := range entries {
		r.priors[priorKey{
			Role: agent.Role(e.Key.Role),
			Tier: e.Key.Tier,
		}] = e.Prior
	}

	return nil
}
