package engine

import "testing"

// TestMonitorSetters_DelegateToOptions is the seam test for the setter
// refactor: every Set* method must be a behaviour-preserving shim over its
// WithMon* option. For each optional dependency we wire one Monitor via the
// imperative Set* method and a second via the functional option, then assert
// the resulting field state is identical. If a future edit makes a setter
// touch a different field (or a second field) than its option, this fails —
// which is the whole point of keeping the assignment logic single-owned in
// options.go.
func TestMonitorSetters_DelegateToOptions(t *testing.T) {
	cr := &ConflictResolver{}
	mgr := &Manager{}
	planner := &Planner{}
	dispatcher := &Dispatcher{}
	executor := &Executor{}
	secGate := &SecurityGate{}
	compGate := &CompletionGate{}

	cases := []struct {
		name   string
		set    func(*Monitor)
		option MonitorOption
		equal  func(a, b *Monitor) bool
	}{
		{
			name:   "ConflictResolver",
			set:    func(m *Monitor) { m.SetConflictResolver(cr) },
			option: WithMonConflictResolver(cr),
			equal:  func(a, b *Monitor) bool { return a.conflictResolver == b.conflictResolver },
		},
		{
			name:   "Manager",
			set:    func(m *Monitor) { m.SetManager(mgr) },
			option: WithMonManager(mgr),
			equal:  func(a, b *Monitor) bool { return a.manager == b.manager },
		},
		{
			name:   "Planner",
			set:    func(m *Monitor) { m.SetPlanner(planner) },
			option: WithMonPlanner(planner),
			equal:  func(a, b *Monitor) bool { return a.planner == b.planner },
		},
		{
			name:   "AutoResume",
			set:    func(m *Monitor) { m.SetAutoResume(dispatcher, executor) },
			option: WithMonAutoResume(dispatcher, executor),
			equal:  func(a, b *Monitor) bool { return a.dispatcher == b.dispatcher && a.executor == b.executor },
		},
		{
			name:   "SecurityGate",
			set:    func(m *Monitor) { m.SetSecurityGate(secGate) },
			option: WithMonSecurityGate(secGate),
			equal:  func(a, b *Monitor) bool { return a.securityGate == b.securityGate },
		},
		{
			name:   "CompletionGate",
			set:    func(m *Monitor) { m.SetCompletionGate(compGate) },
			option: WithMonCompletionGate(compGate),
			equal:  func(a, b *Monitor) bool { return a.completionGate == b.completionGate },
		},
		{
			name:   "DocGenerator",
			set:    func(m *Monitor) { m.SetDocGenerator(nil, "senior-model") },
			option: WithMonDocGenerator(nil, "senior-model"),
			equal:  func(a, b *Monitor) bool { return a.docClient == b.docClient && a.docModel == b.docModel },
		},
		{
			name:   "DryRun",
			set:    func(m *Monitor) { m.SetDryRun(true) },
			option: WithMonDryRun(true),
			equal:  func(a, b *Monitor) bool { return a.dryRun == b.dryRun },
		},
		{
			name:   "MemPalace",
			set:    func(m *Monitor) { m.SetMemPalace(nil) },
			option: WithMonMemPalace(nil),
			equal:  func(a, b *Monitor) bool { return a.mempalace == b.mempalace },
		},
		{
			name:   "ArtifactStore",
			set:    func(m *Monitor) { m.SetArtifactStore(nil) },
			option: WithMonArtifactStore(nil),
			equal:  func(a, b *Monitor) bool { return a.artifactStore == b.artifactStore },
		},
		{
			name:   "BayesianRouter",
			set:    func(m *Monitor) { m.SetBayesianRouter(nil) },
			option: WithMonBayesianRouter(nil),
			equal:  func(a, b *Monitor) bool { return a.bayesian == b.bayesian },
		},
		{
			name:   "CodeGraph",
			set:    func(m *Monitor) { m.SetCodeGraph(nil) },
			option: WithMonCodeGraph(nil),
			equal:  func(a, b *Monitor) bool { return a.codeGraph == b.codeGraph },
		},
		{
			name:   "DevDBLifecycle",
			set:    func(m *Monitor) { m.SetDevDBLifecycle(nil) },
			option: WithMonDevDBLifecycle(nil),
			equal:  func(a, b *Monitor) bool { return a.lifecycle == b.lifecycle },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viaSetter := &Monitor{}
			tc.set(viaSetter)

			viaOption := &Monitor{}
			viaOption.Configure(tc.option)

			if !tc.equal(viaSetter, viaOption) {
				t.Errorf("Set%s and WithMon%s produced different field state", tc.name, tc.name)
			}
		})
	}
}
