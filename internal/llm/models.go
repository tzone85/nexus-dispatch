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
		// Gemma 4 family (recommended)
		{Name: "gemma4:26b", Parameters: "26B (3.8B active)", MinRAMGB: 12, Role: "all", Description: "MoE — best quality/VRAM ratio, native function calling, 256K context"},
		{Name: "gemma4:31b", Parameters: "31B", MinRAMGB: 20, Role: "tech_lead,senior", Description: "Dense — highest quality, needs 24GB+ VRAM"},
		{Name: "gemma4:e4b", Parameters: "4.5B", MinRAMGB: 6, Role: "junior", Description: "Lightweight — fast inference, good for simple tasks"},
		{Name: "gemma4:e2b", Parameters: "2.3B", MinRAMGB: 4, Role: "junior", Description: "Minimal — constrained devices only"},
		// Legacy models (still supported)
		{Name: "deepseek-coder-v2:latest", Parameters: "16B", MinRAMGB: 16, Role: "tech_lead,supervisor", Description: "Strong planning and review — legacy default"},
		{Name: "qwen2.5-coder:32b", Parameters: "32B", MinRAMGB: 24, Role: "senior", Description: "Complex coding — legacy senior default"},
		{Name: "qwen2.5-coder:14b", Parameters: "14B", MinRAMGB: 12, Role: "intermediate,qa", Description: "Balanced coding — legacy mid-tier"},
		{Name: "qwen2.5-coder:7b", Parameters: "7B", MinRAMGB: 8, Role: "junior", Description: "Simple tasks — legacy junior"},
	}
}

// ModelForRole returns the recommended Ollama model name for a given agent
// role. Gemma 4 26B is the universal default for all roles.
func ModelForRole(role string) string {
	return "gemma4:26b"
}
