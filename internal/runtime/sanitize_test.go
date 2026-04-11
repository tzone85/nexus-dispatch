package runtime

import (
	"testing"
)

func TestValidateModelName(t *testing.T) {
	valid := []string{
		"claude-sonnet-4-5-20250514",
		"gemma-3-27b-it",
		"qwen2.5-coder:14b",
		"openai/gpt-4o",
		"anthropic/claude-3.5-sonnet",
		"opus-4",
		"o3",
	}
	for _, name := range valid {
		if err := ValidateModelName(name); err != nil {
			t.Errorf("ValidateModelName(%q) should be valid, got error: %v", name, err)
		}
	}

	invalid := []struct {
		name   string
		reason string
	}{
		{"", "empty string"},
		{"model;evil", "semicolon"},
		{"model$var", "dollar sign"},
		{"model`tick`", "backtick"},
		{"model|pipe", "pipe"},
		{"model&bg", "ampersand"},
		{"model name", "space"},
		{"model\nname", "newline"},
		{"model()", "parentheses"},
		{"model<>", "angle brackets"},
	}
	for _, tc := range invalid {
		if err := ValidateModelName(tc.name); err == nil {
			t.Errorf("ValidateModelName(%q) should be invalid (%s), got nil", tc.name, tc.reason)
		}
	}
}

func TestValidateSessionName(t *testing.T) {
	valid := []string{
		"nxd-abc12345-junior-1",
		"nxd-orphan-story-id",
		"session.name",
		"session_name",
		"ABC123",
	}
	for _, name := range valid {
		if err := ValidateSessionName(name); err != nil {
			t.Errorf("ValidateSessionName(%q) should be valid, got error: %v", name, err)
		}
	}

	invalid := []struct {
		name   string
		reason string
	}{
		{"", "empty string"},
		{"name;evil", "semicolon"},
		{"name$var", "dollar sign"},
		{"name`tick`", "backtick"},
		{"name with space", "space"},
		{"name|pipe", "pipe"},
		{"name&bg", "ampersand"},
		{"name/slash", "slash"},
		{"name:colon", "colon"},
		{"name\ttab", "tab"},
	}
	for _, tc := range invalid {
		if err := ValidateSessionName(tc.name); err == nil {
			t.Errorf("ValidateSessionName(%q) should be invalid (%s), got nil", tc.name, tc.reason)
		}
	}
}

func TestValidateShellArg(t *testing.T) {
	valid := []string{
		"--output-format",
		"json",
		"--max-turns",
		"1",
		"-p",
		"-",
		"simple-arg",
		"path/to/file",
		"file.txt",
	}
	for _, arg := range valid {
		if err := ValidateShellArg(arg); err != nil {
			t.Errorf("ValidateShellArg(%q) should be valid, got error: %v", arg, err)
		}
	}

	invalid := []struct {
		arg    string
		reason string
	}{
		{"arg;evil", "semicolon"},
		{"arg|pipe", "pipe"},
		{"arg&bg", "ampersand"},
		{"arg$(sub)", "dollar with paren"},
		{"arg`tick`", "backtick"},
		{"arg(paren)", "open paren"},
		{"arg)", "close paren"},
		{"arg<redir", "less than"},
		{"arg>redir", "greater than"},
		{"arg{brace", "open brace"},
		{"arg}", "close brace"},
		{"arg!bang", "exclamation"},
		{"arg\\back", "backslash"},
		{"arg\"quote", "double quote"},
		{"arg\nnewline", "newline"},
		{"arg\rreturn", "carriage return"},
	}
	for _, tc := range invalid {
		if err := ValidateShellArg(tc.arg); err == nil {
			t.Errorf("ValidateShellArg(%q) should be invalid (%s), got nil", tc.arg, tc.reason)
		}
	}
}

func TestQuoteShellArg(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"--flag", "--flag"},
		{"no-special-chars", "no-special-chars"},
		{"with space", "'with space'"},
		{"it's", "'it'\\''s'"},
		{"has;semicolon", "'has;semicolon'"},
		{"has$dollar", "'has$dollar'"},
		{"two words here", "'two words here'"},
		{"", "''"},
		{"tab\there", "'tab\there'"},
		{"multi'quote's", "'multi'\\''quote'\\''s'"},
	}
	for _, tc := range tests {
		got := QuoteShellArg(tc.input)
		if got != tc.expected {
			t.Errorf("QuoteShellArg(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
