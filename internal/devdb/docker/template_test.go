//go:build !integration

package docker_test

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestTemplate_TempName_NotEmpty(t *testing.T) {
	got := docker.TempTemplateName("demo-prod-snapshot")
	if got == "" {
		t.Error("TempTemplateName returned empty")
	}
	if got == "demo-prod-snapshot" {
		t.Errorf("TempTemplateName returned input unchanged: %q", got)
	}
	if !strings.HasPrefix(got, "demo-prod-snapshot-tmp-") {
		t.Errorf("TempTemplateName should prefix with '<name>-tmp-', got: %q", got)
	}
}

func TestTemplate_TempName_Unique(t *testing.T) {
	a := docker.TempTemplateName("x")
	b := docker.TempTemplateName("x")
	if a == b {
		t.Errorf("two calls should produce distinct names, got %q both times", a)
	}
}
