package plugin

import "strings"

// PluginPlaybook represents a loaded playbook ready for injection into prompts.
type PluginPlaybook struct {
	Name       string
	Content    string
	InjectWhen string
	Roles      []string
}

// ShouldInject returns true when the playbook's InjectWhen condition matches
// the current dispatch context and the given role is permitted by the Roles filter.
// An empty Roles slice means the playbook applies to all roles.
func (p PluginPlaybook) ShouldInject(role string, isExisting, isBugFix, isInfra bool) bool {
	if !p.matchesCondition(isExisting, isBugFix, isInfra) {
		return false
	}
	return p.matchesRole(role)
}

// matchesCondition checks whether the InjectWhen condition is satisfied.
func (p PluginPlaybook) matchesCondition(isExisting, isBugFix, isInfra bool) bool {
	switch strings.ToLower(strings.TrimSpace(p.InjectWhen)) {
	case "always":
		return true
	case "existing":
		return isExisting
	case "bugfix":
		return isBugFix
	case "infra":
		return isInfra
	default:
		return false
	}
}

// matchesRole checks whether the given role is allowed by the Roles filter.
func (p PluginPlaybook) matchesRole(role string) bool {
	if len(p.Roles) == 0 {
		return true
	}
	lower := strings.ToLower(role)
	for _, r := range p.Roles {
		if strings.ToLower(r) == lower {
			return true
		}
	}
	return false
}
