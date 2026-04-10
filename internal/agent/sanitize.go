package agent

import "strings"

var injectionPrefixes = []string{
	"important:", "ignore", "system:", "instruction:",
	"override:", "disregard", "forget all", "new instructions:",
}

// SanitizePromptField prefixes lines that begin with known prompt-injection
// patterns with a [user-content] tag so the model treats them as data rather
// than instructions. Returns the input unchanged when no injection markers
// are detected.
func SanitizePromptField(input string) string {
	if input == "" {
		return input
	}
	lines := strings.Split(input, "\n")
	modified := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(line))
		for _, prefix := range injectionPrefixes {
			if strings.HasPrefix(trimmed, prefix) {
				lines[i] = "[user-content] " + line
				modified = true
				break
			}
		}
	}
	if !modified {
		return input
	}
	return strings.Join(lines, "\n")
}
