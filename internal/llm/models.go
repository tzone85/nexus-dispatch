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
		// Qwen3-Coder (recommended reviewer/planner — 32GB+ machines)
		{Name: "qwen3-coder:30b", Parameters: "30B (3.3B active)", MinRAMGB: 22, Role: "tech_lead,senior,qa", Description: "MoE reviewer — SWE-bench 51.6%, 262K context, strong root-cause analysis"},
		// Gemma 4 family (recommended coder — native function calling)
		{Name: "gemma4:e4b", Parameters: "4.5B", MinRAMGB: 6, Role: "junior,intermediate,supervisor", Description: "Recommended coder — native function calling, fast, 256K context"},
		{Name: "gemma4:26b", Parameters: "26B (3.8B active)", MinRAMGB: 12, Role: "junior,intermediate", Description: "MoE coder — higher quality than e4b, native function calling, 256K context"},
		{Name: "gemma4:31b", Parameters: "31B", MinRAMGB: 20, Role: "tech_lead,senior", Description: "Dense — highest quality Gemma, needs 24GB+ VRAM"},
		{Name: "gemma4:e2b", Parameters: "2.3B", MinRAMGB: 4, Role: "junior", Description: "Minimal — constrained devices only"},
		// Budget reviewer (24GB machines — qwen3-coder:30b needs 32GB+)
		{Name: "qwen2.5-coder:14b", Parameters: "14B", MinRAMGB: 9, Role: "tech_lead,senior,qa", Description: "Budget reviewer — 128K context, strong structured output, fits on 24GB"},
		// Legacy models (still supported)
		{Name: "deepseek-coder-v2:latest", Parameters: "16B", MinRAMGB: 16, Role: "tech_lead,supervisor", Description: "Strong planning and review — legacy default"},
		{Name: "qwen2.5-coder:32b", Parameters: "32B", MinRAMGB: 24, Role: "senior", Description: "Complex coding — legacy senior default"},
		{Name: "qwen2.5-coder:7b", Parameters: "7B", MinRAMGB: 8, Role: "junior", Description: "Simple tasks — legacy junior"},
	}
}

// ModelForRole returns the recommended Ollama model name for a given agent
// role. Gemma 4 26B is the universal default for all roles.
func ModelForRole(role string) string {
	return "gemma4:26b"
}
