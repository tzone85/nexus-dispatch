package git

import (
	"os"
	"path/filepath"
)

// TechStack describes the detected language, framework, and build tool of a
// repository.
type TechStack struct {
	Language  string
	Framework string
	BuildTool string
}

// ScanRepo inspects the given directory for well-known marker files and returns
// the detected tech stack.
func ScanRepo(repoDir string) TechStack {
	stack := TechStack{}

	type marker struct {
		file  string
		apply func(*TechStack)
	}

	// Ordered so that more specific markers (e.g. tsconfig) win over generic
	// ones (e.g. package.json) when both are present.
	markers := []marker{
		{"go.mod", func(s *TechStack) { s.Language = "go"; s.BuildTool = "go" }},
		{"Cargo.toml", func(s *TechStack) { s.Language = "rust"; s.BuildTool = "cargo" }},
		{"pom.xml", func(s *TechStack) { s.Language = "java"; s.BuildTool = "maven" }},
		{"build.gradle", func(s *TechStack) { s.Language = "java"; s.BuildTool = "gradle" }},
		{"pyproject.toml", func(s *TechStack) { s.Language = "python"; s.BuildTool = "poetry" }},
		{"requirements.txt", func(s *TechStack) { s.Language = "python"; s.BuildTool = "pip" }},
		{"package.json", func(s *TechStack) { s.Language = "javascript"; s.BuildTool = "npm" }},
		{"tsconfig.json", func(s *TechStack) { s.Language = "typescript"; s.BuildTool = "npm" }},
	}

	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(repoDir, m.file)); err == nil {
			m.apply(&stack)
		}
	}

	return stack
}
