package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsHallucinationLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected bool
	}{
		// Hallucination patterns
		{"Looking at", "Looking at the code, I can see...", true},
		{"Let me", "Let me fix this issue", true},
		{"I'll", "I'll start by reading the file", true},
		{"I've", "I've analyzed the problem", true},
		{"I can see", "I can see that the function is broken", true},
		{"I need to", "I need to understand the requirements", true},
		{"I will", "I will implement the function", true},
		{"Here's", "Here's the fixed code", true},
		{"Here is", "Here is the solution", true},
		{"Based on", "Based on the error message", true},
		{"Now I", "Now I need to check the tests", true},
		{"Now let me", "Now let me implement this", true},
		{"The following", "The following code shows", true},
		{"This is the", "This is the correct approach", true},
		{"**Key", "**Key insight**: the bug is here", true},
		{"**Resolution", "**Resolution**: fix the null pointer", true},
		{"## ", "## Analysis", true},
		{"# Resolution", "# Resolution", true},
		{"Sure,", "Sure, I can help with that", true},
		{"Sure!", "Sure! Let's fix this", true},
		{"Absolutely", "Absolutely, this needs fixing", true},
		{"Great,", "Great, let's start", true},
		{"OK,", "OK, I understand", true},
		{"Okay,", "Okay, let me check", true},
		{"numbered list", "1. **First step**: analyze", true},
		{"numbered list 2", "2. **Second step**: implement", true},
		{"numbered list 10", "10. **Final step**: test", true},

		// Valid code examples
		{"function def", "func main() {", false},
		{"variable", "var x = 5", false},
		{"import", "import \"fmt\"", false},
		{"comment", "// This is a comment", false},
		{"class", "class MyClass {", false},
		{"html", "<div>Hello</div>", false},
		{"json", "{\"key\": \"value\"}", false},
		{"empty", "", false},
		{"whitespace only", "   \t  ", false},
		{"normal sentence", "This is just a regular sentence without patterns.", false},
		{"starts with digit", "123 is a number", false},
		{"looks like list but not pattern", "1. This is not a pattern", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHallucinationLine(tt.line)
			if got != tt.expected {
				t.Errorf("isHallucinationLine(%q) = %v, want %v", tt.line, got, tt.expected)
			}
		})
	}
}

func TestIsSourceExt(t *testing.T) {
	tests := []struct {
		name     string
		ext      string
		expected bool
	}{
		// Source extensions
		{"javascript", ".js", true},
		{"typescript", ".ts", true},
		{"tsx", ".tsx", true},
		{"jsx", ".jsx", true},
		{"python", ".py", true},
		{"go", ".go", true},
		{"rust", ".rs", true},
		{"java", ".java", true},
		{"ruby", ".rb", true},
		{"vue", ".vue", true},
		{"svelte", ".svelte", true},
		{"css", ".css", true},
		{"scss", ".scss", true},
		{"html", ".html", true},
		{"json", ".json", true},
		{"yaml", ".yaml", true},
		{"yml", ".yml", true},
		{"shell", ".sh", true},
		{"bash", ".bash", true},
		{"zsh", ".zsh", true},
		{"fish", ".fish", true},
		{"sql", ".sql", true},
		{"graphql", ".graphql", true},
		{"proto", ".proto", true},

		// Non-source extensions
		{"text", ".txt", false},
		{"markdown", ".md", false},
		{"image", ".png", false},
		{"binary", ".exe", false},
		{"archive", ".zip", false},
		{"video", ".mp4", false},
		{"empty", "", false},
		{"no dot", "js", false},

		// Case insensitive
		{"uppercase", ".JS", true},
		{"mixed case", ".Go", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSourceExt(tt.ext)
			if got != tt.expected {
				t.Errorf("isSourceExt(%q) = %v, want %v", tt.ext, got, tt.expected)
			}
		})
	}
}

func TestScrubFile(t *testing.T) {
	// Create a temporary file with hallucination preamble
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.go")

	content := `Looking at the code, I can see the issue.
Let me fix this problem for you.

func main() {
	fmt.Println("Hello, World!")
}
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Scrub the file
	modified, err := scrubFile(tmpFile)
	if err != nil {
		t.Fatalf("scrubFile failed: %v", err)
	}

	if !modified {
		t.Error("Expected file to be modified")
	}

	// Verify the content was cleaned
	cleanedContent, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read cleaned file: %v", err)
	}

	expected := `func main() {
	fmt.Println("Hello, World!")
}`

	if string(cleanedContent) != expected {
		t.Errorf("Cleaned content = %q, want %q", string(cleanedContent), expected)
	}
}

func TestScrubFile_NoHallucination(t *testing.T) {
	// Create a temporary file without hallucination preamble
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "clean.go")

	content := `func main() {
	fmt.Println("Hello, World!")
}`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Scrub the file
	modified, err := scrubFile(tmpFile)
	if err != nil {
		t.Fatalf("scrubFile failed: %v", err)
	}

	if modified {
		t.Error("Expected file not to be modified")
	}

	// Verify the content is unchanged
	finalContent, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(finalContent) != content {
		t.Error("File content should not have changed")
	}
}

func TestScrubFile_AllHallucination(t *testing.T) {
	// Create a temporary file with only hallucination lines
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "all_hallucination.go")

	content := `Looking at the code, I can see the issue.
Let me fix this problem for you.
Based on the requirements, here's what I found.`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Scrub the file - should refuse to strip all content
	modified, err := scrubFile(tmpFile)
	if err != nil {
		t.Fatalf("scrubFile failed: %v", err)
	}

	if modified {
		t.Error("Expected file not to be modified when all lines are hallucinations")
	}

	// Verify the content is unchanged
	finalContent, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(finalContent) != content {
		t.Error("File content should not have changed")
	}
}

func TestScanFileForConflictMarkers(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name: "no conflicts",
			content: `func main() {
	fmt.Println("Hello")
}`,
			expected: false,
		},
		{
			name: "has opening marker",
			content: `func main() {
<<<<<<< HEAD
	fmt.Println("Hello")
=======
	fmt.Println("Hi")
>>>>>>> branch
}`,
			expected: true,
		},
		{
			name: "has separator marker",
			content: `func main() {
	fmt.Println("Hello")
=======
	fmt.Println("Hi")
}`,
			expected: true,
		},
		{
			name: "has closing marker",
			content: `func main() {
	fmt.Println("Hello")
>>>>>>> branch
}`,
			expected: true,
		},
		{
			name: "markers with whitespace",
			content: `func main() {
  <<<<<<< HEAD
	fmt.Println("Hello")
}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.go")

			err := os.WriteFile(tmpFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			hasConflicts, err := scanFileForConflictMarkers(tmpFile)
			if err != nil {
				t.Fatalf("scanFileForConflictMarkers failed: %v", err)
			}

			if hasConflicts != tt.expected {
				t.Errorf("scanFileForConflictMarkers() = %v, want %v", hasConflicts, tt.expected)
			}
		})
	}
}

func TestValidateBuild_NonexistentDir(t *testing.T) {
	nonexistentDir := "/path/that/does/not/exist"
	err := validateBuild(nonexistentDir)
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
	if err.Error() != "no recognized project type found (package.json, go.mod, pyproject.toml)" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestValidateBuild_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	err := validateBuild(tmpDir)
	if err == nil {
		t.Error("Expected error for directory with no project files")
	}
	if err.Error() != "no recognized project type found (package.json, go.mod, pyproject.toml)" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestValidateBuild_GoProject(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal go.mod
	goMod := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Create a simple Go file that should build
	goFile := filepath.Join(tmpDir, "main.go")
	err = os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create main.go: %v", err)
	}

	err = validateBuild(tmpDir)
	if err != nil {
		t.Errorf("Expected Go project to build successfully, got error: %v", err)
	}
}