package improver

import "testing"

// TestSeverityRank_OrdersAllLevels guards the dashboard ordering invariant:
// critical > warning > info, with anything unknown falling to the bottom.
// Without this test, severityRank could silently drop a future Severity
// constant from the sort key and the dashboard would render
// "info" suggestions above "critical" ones.
func TestSeverityRank_OrdersAllLevels(t *testing.T) {
	cases := []struct {
		sev  Severity
		want int
	}{
		{SeverityCritical, 3},
		{SeverityWarning, 2},
		{SeverityInfo, 1},
		{Severity(""), 0},          // zero-value
		{Severity("disaster"), 0},  // any unknown
	}
	for _, tc := range cases {
		t.Run(string(tc.sev), func(t *testing.T) {
			if got := severityRank(tc.sev); got != tc.want {
				t.Errorf("severityRank(%q) = %d, want %d", tc.sev, got, tc.want)
			}
		})
	}
}

// TestSeverityForRatio_BoundaryConditions covers the threshold logic the
// metrics analyzer uses to choose "critical" vs "warning" — the
// boundary at exactly 2x is the interesting case (must produce
// critical, not warning).
func TestSeverityForRatio_BoundaryConditions(t *testing.T) {
	cases := []struct {
		name     string
		actual   float64
		thresh   float64
		want     Severity
	}{
		{"exactly_2x_threshold_is_critical", 50, 25, SeverityCritical},
		{"just_above_threshold_is_warning", 26, 25, SeverityWarning},
		{"well_above_2x_is_critical", 100, 25, SeverityCritical},
		{"zero_threshold_falls_through_to_warning", 50, 0, SeverityWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := severityForRatio(tc.actual, tc.thresh); got != tc.want {
				t.Errorf("severityForRatio(%v, %v) = %s, want %s", tc.actual, tc.thresh, got, tc.want)
			}
		})
	}
}

// TestAnalyzerName_Returns covers the cheap accessor methods so a future
// rename or refactor doesn't silently change the public Name() contract
// the analyzer registry depends on (TestImprover_RunRespectsCtxCancellation
// uses AnalyzerFunc.Name in its Label field).
func TestAnalyzerName_Returns(t *testing.T) {
	m := MetricsAnalyzer{}
	if m.Name() != "metrics" {
		t.Errorf("MetricsAnalyzer.Name = %q, want metrics", m.Name())
	}

	a := AnalyzerFunc{Label: "custom-x"}
	if a.Name() != "custom-x" {
		t.Errorf("AnalyzerFunc.Name = %q, want custom-x", a.Name())
	}
}
