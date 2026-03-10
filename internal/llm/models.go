package llm

// LocalModel describes a local model suitable for a specific agent role.
type LocalModel struct {
	Name        string // Ollama model name (e.g., "deepseek-coder-v2:latest")
	Parameters  string // Size (e.g., "16B", "33B", "70B")
	MinRAMGB    int    // Minimum RAM needed in GB
	Role        string // Recommended role: "tech_lead", "senior", "intermediate", "junior", "qa", "supervisor"
	Description string // Human-readable description of the model's strengths
}

// RecommendedModels returns local models suitable for each agent role.
func RecommendedModels() []LocalModel {
	return []LocalModel{
		{
			Name:        "deepseek-coder-v2:latest",
			Parameters:  "16B",
			MinRAMGB:    16,
			Role:        "tech_lead",
			Description: "Strong reasoning for planning and decomposition",
		},
		{
			Name:        "qwen2.5-coder:32b",
			Parameters:  "32B",
			MinRAMGB:    24,
			Role:        "senior",
			Description: "Code review and complex implementation",
		},
		{
			Name:        "qwen2.5-coder:14b",
			Parameters:  "14B",
			MinRAMGB:    12,
			Role:        "intermediate",
			Description: "Mid-complexity implementation tasks",
		},
		{
			Name:        "qwen2.5-coder:7b",
			Parameters:  "7B",
			MinRAMGB:    8,
			Role:        "junior",
			Description: "Simple implementation tasks",
		},
		{
			Name:        "qwen2.5-coder:14b",
			Parameters:  "14B",
			MinRAMGB:    12,
			Role:        "qa",
			Description: "Test analysis and quality assessment",
		},
		{
			Name:        "deepseek-coder-v2:latest",
			Parameters:  "16B",
			MinRAMGB:    16,
			Role:        "supervisor",
			Description: "Progress review and drift detection",
		},
	}
}

// ModelForRole returns the recommended Ollama model name for a given agent
// role. Returns an empty string if the role is not recognized.
func ModelForRole(role string) string {
	for _, m := range RecommendedModels() {
		if m.Role == role {
			return m.Name
		}
	}
	return ""
}
