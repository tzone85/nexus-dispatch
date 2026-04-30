package engine

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

var defaultMethCfg = config.MethodologyConfig{DDD: true, TDD: true, MinCoveragePct: 80, AllowOverride: true}

func TestResolveMethodology_DefaultsApply(t *testing.T) {
	d := ResolveMethodology(defaultMethCfg, "Add a health endpoint")
	if !d.DDD || !d.TDD {
		t.Errorf("expected DDD+TDD default, got %+v", d)
	}
	if d.Source != "config" {
		t.Errorf("source = %q, want config", d.Source)
	}
}

func TestResolveMethodology_RelaxedOverride(t *testing.T) {
	for _, val := range []string{"relaxed", "RELAXED", "none", "off"} {
		d := ResolveMethodology(defaultMethCfg, "methodology: "+val+"\n\nQuick prototype.")
		if d.DDD || d.TDD {
			t.Errorf("override=%q: expected both off, got %+v", val, d)
		}
		if !strings.HasPrefix(d.Source, "override:") {
			t.Errorf("override=%q: source should start with override:, got %q", val, d.Source)
		}
	}
}

func TestResolveMethodology_TDDOnly(t *testing.T) {
	d := ResolveMethodology(defaultMethCfg, "methodology: tdd-only\nfix the parser")
	if d.DDD || !d.TDD {
		t.Errorf("expected TDD-only, got %+v", d)
	}
}

func TestResolveMethodology_DDDOnly(t *testing.T) {
	d := ResolveMethodology(defaultMethCfg, "methodology: ddd-only\nrefactor")
	if !d.DDD || d.TDD {
		t.Errorf("expected DDD-only, got %+v", d)
	}
}

func TestResolveMethodology_OverrideDisallowed(t *testing.T) {
	cfg := defaultMethCfg
	cfg.AllowOverride = false
	d := ResolveMethodology(cfg, "methodology: relaxed\nQuick prototype.")
	if !d.DDD || !d.TDD {
		t.Errorf("override should be ignored when AllowOverride=false: %+v", d)
	}
	if d.Source != "config" {
		t.Errorf("source = %q, want config (override blocked)", d.Source)
	}
}

func TestResolveMethodology_NoOverrideTokenInText(t *testing.T) {
	d := ResolveMethodology(defaultMethCfg, "Build a feature that uses TDD style.")
	// "TDD style" alone shouldn't trigger override — only the directive does.
	if !d.DDD || !d.TDD {
		t.Errorf("expected defaults, mistakenly matched a non-directive: %+v", d)
	}
}

func TestBuildMethodologyDirective_BothOn(t *testing.T) {
	got := buildMethodologyDirective(defaultMethCfg, "any req")
	if !strings.Contains(got, "Domain-Driven Design") || !strings.Contains(got, "Test-Driven Development") {
		t.Errorf("missing DDD/TDD sections: %s", got)
	}
}

func TestBuildMethodologyDirective_Relaxed(t *testing.T) {
	got := buildMethodologyDirective(defaultMethCfg, "methodology: relaxed\nstuff")
	if got != "" {
		t.Errorf("relaxed override should produce empty directive, got: %s", got)
	}
}

func TestBuildMethodologyDirective_TDDOnlyOmitsDDD(t *testing.T) {
	got := buildMethodologyDirective(defaultMethCfg, "methodology: tdd-only\nstuff")
	if strings.Contains(got, "Domain-Driven Design") {
		t.Errorf("tdd-only should not include DDD section: %s", got)
	}
	if !strings.Contains(got, "Test-Driven Development") {
		t.Errorf("tdd-only should include TDD section: %s", got)
	}
}

func TestBuildMethodologyDirective_DDDOnlyOmitsTDD(t *testing.T) {
	got := buildMethodologyDirective(defaultMethCfg, "methodology: ddd-only\nstuff")
	if !strings.Contains(got, "Domain-Driven Design") {
		t.Errorf("ddd-only should include DDD: %s", got)
	}
	if strings.Contains(got, "Test-Driven Development") {
		t.Errorf("ddd-only should not include TDD: %s", got)
	}
}

func TestBuildMethodologyDirective_BothOff_FromConfig(t *testing.T) {
	cfg := config.MethodologyConfig{DDD: false, TDD: false, AllowOverride: true}
	got := buildMethodologyDirective(cfg, "any req")
	if got != "" {
		t.Errorf("expected empty directive when both off in config, got: %s", got)
	}
}
