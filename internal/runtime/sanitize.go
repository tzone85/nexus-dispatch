package runtime

import (
	"fmt"
	"regexp"
	"strings"
)

// modelNamePattern matches valid model name characters: alphanumeric, dots,
// underscores, colons, slashes, and hyphens.
var modelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`)

// sessionNamePattern matches valid tmux session name characters: alphanumeric,
// dots, underscores, and hyphens.
var sessionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// shellMetachars contains characters that are unsafe in shell arguments.
const shellMetachars = ";|&$`()<>{}!\\\"\n\r"

// ValidateModelName checks that a model name contains only safe characters.
// It rejects empty strings and any name containing shell metacharacters.
func ValidateModelName(model string) error {
	if model == "" {
		return fmt.Errorf("model name must not be empty")
	}
	if !modelNamePattern.MatchString(model) {
		return fmt.Errorf("model name %q contains invalid characters (allowed: a-z A-Z 0-9 . _ : / -)", model)
	}
	return nil
}

// ValidateSessionName checks that a tmux session name contains only safe
// characters. It rejects empty strings and any name containing shell
// metacharacters, spaces, or path separators.
func ValidateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name must not be empty")
	}
	if !sessionNamePattern.MatchString(name) {
		return fmt.Errorf("session name %q contains invalid characters (allowed: a-z A-Z 0-9 . _ -)", name)
	}
	return nil
}

// ValidateShellArg rejects arguments containing shell metacharacters that
// could enable injection when interpolated into shell command strings.
func ValidateShellArg(arg string) error {
	for _, c := range arg {
		if strings.ContainsRune(shellMetachars, c) {
			return fmt.Errorf("shell argument %q contains unsafe character %q", arg, string(c))
		}
	}
	return nil
}

// safeShellArgPattern matches arguments that need no quoting: alphanumeric
// characters plus a handful of safe punctuation.
var safeShellArgPattern = regexp.MustCompile(`^[a-zA-Z0-9._:/@=,+-]+$`)

// QuoteShellArg returns a shell-safe version of the argument using single
// quotes. If the argument contains only safe characters, it is returned
// unchanged. Embedded single quotes are escaped with the '\'' idiom.
func QuoteShellArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if safeShellArgPattern.MatchString(arg) {
		return arg
	}
	// Wrap in single quotes, escaping any embedded single quotes.
	// The '\'' idiom: end current quote, add escaped quote, reopen quote.
	escaped := strings.ReplaceAll(arg, "'", "'\\''")
	return "'" + escaped + "'"
}
