// Package agent defines agent roles, complexity-based routing, system prompt
// templates, and reputation scoring for the NXD orchestration system.
package agent

import "github.com/tzone85/nexus-dispatch/internal/config"

// Role identifies an agent's function within the development team.
type Role string

const (
	RoleTechLead     Role = "tech_lead"
	RoleSenior       Role = "senior"
	RoleIntermediate Role = "intermediate"
	RoleJunior       Role = "junior"
	RoleQA           Role = "qa"
	RoleSupervisor   Role = "supervisor"
	RoleManager      Role = "manager"
)

// String returns the role as a plain string.
func (r Role) String() string {
	return string(r)
}

// RouteByComplexity determines which role should handle a story based on
// its complexity score and the routing thresholds from configuration.
func RouteByComplexity(complexity int, routing config.RoutingConfig) Role {
	if complexity <= routing.JuniorMaxComplexity {
		return RoleJunior
	}
	if complexity <= routing.IntermediateMaxComplexity {
		return RoleIntermediate
	}
	return RoleSenior
}

// ExecutionMode describes how an agent role interacts with the system.
type ExecutionMode string

const (
	ExecAPI    ExecutionMode = "api"
	ExecCLI    ExecutionMode = "cli"
	ExecHybrid ExecutionMode = "hybrid" // API + shell commands
)

// ExecutionMode returns the default execution mode for this role.
func (r Role) ExecutionMode() ExecutionMode {
	switch r {
	case RoleTechLead, RoleSupervisor:
		return ExecAPI
	case RoleSenior:
		return ExecAPI
	case RoleManager:
		return ExecAPI
	case RoleIntermediate, RoleJunior:
		return ExecCLI
	case RoleQA:
		return ExecHybrid
	}
	return ExecAPI
}

// ModelConfig returns the model configuration for this role from the
// provided models config.
func (r Role) ModelConfig(models config.ModelsConfig) config.ModelConfig {
	switch r {
	case RoleTechLead:
		return models.TechLead
	case RoleSenior:
		return models.Senior
	case RoleIntermediate:
		return models.Intermediate
	case RoleJunior:
		return models.Junior
	case RoleQA:
		return models.QA
	case RoleSupervisor:
		return models.Supervisor
	case RoleManager:
		return models.Manager
	}
	return models.Junior
}
