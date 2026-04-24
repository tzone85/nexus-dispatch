package engine

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// isHallucinationLine checks if a line contains LLM preamble patterns that should be stripped.
// Returns true for common AI assistant intro patterns like "Looking at", "Let me", etc.
func isHallucinationLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	// Common LLM preamble patterns
	patterns := []string{
		"Looking at ", "Let me ", "I'll ", "I've ", "I can see ", "I need to ",
		"I will ", "Here's ", "Here is ", "Based on ", "Now I ", "Now let me ",
		"The following ", "This is the ", "**Key ", "**Resolution", "## ",
		"# Resolution", "Sure,", "Sure!", "Absolutely", "Great,", "OK,", "Okay,",
	}

	for _, pattern := range patterns {
		if strings.HasPrefix(line, pattern) {
			return true
		}
	}

	// Check for numbered list patterns like "1. **...:.*"
	numberedListPattern := regexp.MustCompile(`^\d+\.\s*\*\*.*:`)
	return numberedListPattern.MatchString(line)
}

// isSourceExt returns true if the file extension indicates a source code file.
func isSourceExt(ext string) bool {
	sourceExts := map[string]bool{
		".js":      true,
		".ts":      true,
		".tsx":     true,
		".jsx":     true,
		".py":      true,
		".go":      true,
		".rs":      true,
		".java":    true,
		".rb":      true,
		".vue":     true,
		".svelte":  true,
		".css":     true,
		".scss":    true,
		".html":    true,
		".json":    true,
		".yaml":    true,
		".yml":     true,
		".sh":      true,
		".bash":    true,
		".zsh":     true,
		".fish":    true,
		".sql":     true,
		".graphql": true,
		".proto":   true,
	}
	return sourceExts[strings.ToLower(ext)]
}

// scrubFile reads a file, strips leading hallucination lines, and writes it back.
// Returns true if the file was modified, false otherwise.
// Refuses to strip if it would remove all content.
func scrubFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	if len(lines) == 0 {
		return false, nil
	}

	// Find first non-hallucination, non-empty line
	firstValidLine := 0
	for i, line := range lines {
		if !isHallucinationLine(line) && strings.TrimSpace(line) != "" {
			firstValidLine = i
			break
		}
		// If all lines are hallucinations or empty, refuse to strip
		if i == len(lines)-1 {
			return false, nil
		}
	}

	// No hallucination lines found at the beginning
	if firstValidLine == 0 {
		return false, nil
	}

	// Write the cleaned content back
	cleanedLines := lines[firstValidLine:]
	output := strings.Join(cleanedLines, "\n")

	err = os.WriteFile(path, []byte(output), 0644)
	if err != nil {
		return false, fmt.Errorf("failed to write cleaned file %s: %w", path, err)
	}

	return true, nil
}

// scrubHallucinationsFromWorktree processes all changed files in the worktree,
// strips hallucination lines from source files, re-stages and amends the commit.
// Returns the count of files that were cleaned.
func scrubHallucinationsFromWorktree(worktreePath string) (int, error) {
	// Get list of changed files
	cmd := exec.Command("git", "diff", "--name-only", "HEAD~1")
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get changed files: %w", err)
	}

	changedFiles := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(changedFiles) == 1 && changedFiles[0] == "" {
		return 0, nil // No changed files
	}

	cleanedCount := 0
	var cleanedFiles []string

	// Process each changed file
	for _, relPath := range changedFiles {
		if relPath == "" {
			continue
		}

		ext := filepath.Ext(relPath)
		if !isSourceExt(ext) {
			continue
		}

		fullPath := filepath.Join(worktreePath, relPath)
		cleaned, err := scrubFile(fullPath)
		if err != nil {
			return cleanedCount, fmt.Errorf("failed to scrub file %s: %w", relPath, err)
		}

		if cleaned {
			cleanedCount++
			cleanedFiles = append(cleanedFiles, relPath)
		}
	}

	// Re-stage and amend commit if any files were cleaned
	if cleanedCount > 0 {
		// Re-stage cleaned files
		for _, file := range cleanedFiles {
			cmd := exec.Command("git", "add", file)
			cmd.Dir = worktreePath
			if err := cmd.Run(); err != nil {
				return cleanedCount, fmt.Errorf("failed to re-stage file %s: %w", file, err)
			}
		}

		// Amend the commit
		cmd := exec.Command("git", "commit", "--amend", "--no-edit")
		cmd.Dir = worktreePath
		if err := cmd.Run(); err != nil {
			return cleanedCount, fmt.Errorf("failed to amend commit: %w", err)
		}
	}

	return cleanedCount, nil
}

// scanFileForConflictMarkers scans a file for git conflict markers.
// Returns true if any conflict markers are found.
func scanFileForConflictMarkers(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "<<<<<<<") ||
		   strings.HasPrefix(line, "=======") ||
		   strings.HasPrefix(line, ">>>>>>>") {
			return true, nil
		}
	}

	return false, scanner.Err()
}

// validateNoConflictMarkers scans all changed files in the worktree for conflict markers.
// Returns a list of file paths that contain unresolved conflict markers.
func validateNoConflictMarkers(worktreePath string) ([]string, error) {
	// Get list of changed files
	cmd := exec.Command("git", "diff", "--name-only", "HEAD~1")
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get changed files: %w", err)
	}

	changedFiles := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(changedFiles) == 1 && changedFiles[0] == "" {
		return nil, nil // No changed files
	}

	var filesWithConflicts []string

	for _, relPath := range changedFiles {
		if relPath == "" {
			continue
		}

		fullPath := filepath.Join(worktreePath, relPath)
		hasConflicts, err := scanFileForConflictMarkers(fullPath)
		if err != nil {
			// Skip files that can't be read (may have been deleted)
			continue
		}

		if hasConflicts {
			filesWithConflicts = append(filesWithConflicts, relPath)
		}
	}

	return filesWithConflicts, nil
}

// validateBuild detects the project type and runs the appropriate build command.
// Returns an error with output truncated to 500 characters if the build fails.
func validateBuild(worktreePath string) error {
	// Check for different project types
	if _, err := os.Stat(filepath.Join(worktreePath, "package.json")); err == nil {
		return validateNodeBuild(worktreePath)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "go.mod")); err == nil {
		return validateGoBuild(worktreePath)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "pyproject.toml")); err == nil {
		return validatePythonBuild(worktreePath)
	}

	return fmt.Errorf("no recognized project type found (package.json, go.mod, pyproject.toml)")
}

// validateNodeBuild runs npm build or TypeScript check for Node.js projects.
func validateNodeBuild(worktreePath string) error {
	// Try npm run build first
	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err == nil {
		return nil
	}

	// If npm run build fails, try tsc --noEmit for TypeScript projects
	if _, tsErr := os.Stat(filepath.Join(worktreePath, "tsconfig.json")); tsErr == nil {
		cmd = exec.Command("tsc", "--noEmit")
		cmd.Dir = worktreePath
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		truncated := truncateOutput(string(output), 500)
		return fmt.Errorf("build failed: %s", truncated)
	}

	return nil
}

// validateGoBuild runs go build for Go projects.
func validateGoBuild(worktreePath string) error {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		truncated := truncateOutput(string(output), 500)
		return fmt.Errorf("go build failed: %s", truncated)
	}

	return nil
}

// validatePythonBuild runs syntax check for Python projects.
func validatePythonBuild(worktreePath string) error {
	cmd := exec.Command("python", "-m", "py_compile", ".")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		truncated := truncateOutput(string(output), 500)
		return fmt.Errorf("python syntax check failed: %s", truncated)
	}

	return nil
}

// truncateOutput truncates output to maxLength characters, adding "..." if truncated.
func truncateOutput(output string, maxLength int) string {
	if len(output) <= maxLength {
		return output
	}
	return output[:maxLength-3] + "..."
}