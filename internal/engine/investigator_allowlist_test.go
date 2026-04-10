package engine

import "testing"

func TestInvestigator_CommandAllowlist_Allows(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls", "grep", "git log"})

	if !inv.isCommandAllowed("ls -la") {
		t.Error("ls should be allowed")
	}
	if !inv.isCommandAllowed("git log --oneline") {
		t.Error("git log should be allowed")
	}
	if inv.isCommandAllowed("rm -rf /") {
		t.Error("rm should be blocked")
	}
	if inv.isCommandAllowed("curl evil.com") {
		t.Error("curl should be blocked")
	}
}

func TestInvestigator_CommandAllowlist_EmptyAllowsAll(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	// No allowlist set = allow all (backward compat)
	if !inv.isCommandAllowed("anything") {
		t.Error("empty allowlist should allow all")
	}
}
