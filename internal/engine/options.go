package engine

import (
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/codegraph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/routing"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
)

// ExecutorOption configures an Executor at construction time. Functional
// options replace the imperative SetX builder methods so wiring code reads
// like a parameter list and tests can declare "all the optional state I
// care about" in one place.
//
// The SetX methods remain as thin shims so existing call sites and tests
// keep working — new code should prefer Configure / NewExecutorWithOptions.
type ExecutorOption func(*Executor)

// WithExecLLMClient sets the LLM client used by native runtimes.
func WithExecLLMClient(c llm.Client) ExecutorOption {
	return func(e *Executor) { e.llmClient = c }
}

// WithExecArtifactStore wires the per-story artifact store.
func WithExecArtifactStore(s *artifact.Store) ExecutorOption {
	return func(e *Executor) { e.artifactStore = s }
}

// WithExecScratchboard wires the shared scratchboard.
func WithExecScratchboard(sb *scratchboard.Scratchboard) ExecutorOption {
	return func(e *Executor) { e.scratchboard = sb }
}

// WithExecController wires the periodic controller for cancel/restart hooks.
func WithExecController(c *Controller) ExecutorOption {
	return func(e *Executor) { e.controller = c }
}

// WithExecDirectiveStore wires the operator-directive store.
func WithExecDirectiveStore(d *DirectiveStore) ExecutorOption {
	return func(e *Executor) { e.directives = d }
}

// WithExecProjectDir wires the project state directory used for RepoProfile
// lookup.
func WithExecProjectDir(dir string) ExecutorOption {
	return func(e *Executor) { e.projectDir = dir }
}

// Configure applies the given options. Useful when extending an existing
// Executor (e.g. tests that want to set just one knob).
func (e *Executor) Configure(opts ...ExecutorOption) {
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
}

// MonitorOption configures a Monitor at construction time. See
// ExecutorOption for the rationale.
type MonitorOption func(*Monitor)

// WithMonMemPalace wires the MemPalace knowledge base.
func WithMonMemPalace(mp *memory.MemPalace) MonitorOption {
	return func(m *Monitor) { m.mempalace = mp }
}

// WithMonArtifactStore wires the per-story artifact store.
func WithMonArtifactStore(s *artifact.Store) MonitorOption {
	return func(m *Monitor) { m.artifactStore = s }
}

// WithMonBayesianRouter wires the Bayesian role router.
func WithMonBayesianRouter(r *routing.BayesianRouter) MonitorOption {
	return func(m *Monitor) { m.bayesian = r }
}

// WithMonCodeGraph wires the blast-radius analyzer used during review.
func WithMonCodeGraph(cg *codegraph.Runner) MonitorOption {
	return func(m *Monitor) { m.codeGraph = cg }
}

// WithMonConflictResolver wires the LLM-powered merge conflict resolver.
func WithMonConflictResolver(cr *ConflictResolver) MonitorOption {
	return func(m *Monitor) { m.conflictResolver = cr }
}

// WithMonAutoResume wires dispatch + executor for automatic next-wave
// dispatch when a wave completes. Auto-resume is implicit: it activates
// whenever both the dispatcher and executor are set on the Monitor.
func WithMonAutoResume(d *Dispatcher, e *Executor) MonitorOption {
	return func(m *Monitor) {
		m.dispatcher = d
		m.executor = e
	}
}

// WithMonManager wires the supervisor manager for stuck-story escalation.
func WithMonManager(mgr *Manager) MonitorOption {
	return func(m *Monitor) { m.manager = mgr }
}

// WithMonPlanner wires the planner used by the manager for split decisions.
func WithMonPlanner(p *Planner) MonitorOption {
	return func(m *Monitor) { m.planner = p }
}

// WithMonDryRun toggles dry-run mode. In dry-run the monitor emits the same
// events but skips side effects with external state (PR creation, push).
func WithMonDryRun(enabled bool) MonitorOption {
	return func(m *Monitor) { m.dryRun = enabled }
}

// Configure applies the given options to an already-constructed Monitor.
func (m *Monitor) Configure(opts ...MonitorOption) {
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
}
