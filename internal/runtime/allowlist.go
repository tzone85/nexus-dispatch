package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// forbiddenCommandChars is the set of shell metacharacters that are rejected
// outright from agent-supplied commands: chaining (;&|), expansion ($`),
// redirection (<>), control characters, NUL and backslash escapes. Commands
// are executed argv-style (no shell) after tokenization, but rejecting these
// keeps the model from even attempting shell tricks and makes the intent of
// a rejected command obvious in the tool result.
const forbiddenCommandChars = ";&|$`<>\n\r\t\x00\\"

// ErrEmptyAllowlist is returned when no allowlist entries are configured.
// An empty allowlist denies everything — it is never "allow all".
var ErrEmptyAllowlist = errors.New("command allowlist is empty: all commands denied")

// execFlags are flags that make an otherwise-allowlisted binary run an
// arbitrary program (find -exec, go test -exec, go build -toolexec, ...).
// They are rejected as whole tokens and in --flag=value form regardless of
// which binary precedes them.
var execFlags = []string{
	"-exec", "--exec", "-execdir", "--execdir", "-ok", "-okdir",
	"-toolexec", "--toolexec",
}

// pathFlags take a filesystem path as their value (either the next token or
// attached: -f=x, --file=x, -Cdir). The value must stay inside the worktree.
var pathFlags = []string{"-f", "--file", "-C", "--directory", "-o", "--output"}

// TokenizeCommand splits a command string into argv tokens using shell-word
// rules (single quotes, double quotes, whitespace separation) WITHOUT any
// expansion. Metacharacters from forbiddenCommandChars are rejected before
// tokenization, so quoting can never smuggle them through.
func TokenizeCommand(command string) ([]string, error) {
	if strings.ContainsAny(command, forbiddenCommandChars) {
		return nil, fmt.Errorf("command contains a forbidden shell metacharacter")
	}
	var (
		tokens []string
		cur    strings.Builder
		inTok  bool
		quote  rune // 0, '\'' or '"'
	)
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inTok = true
		case r == ' ':
			if inTok {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteRune(r)
			inTok = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("command has an unterminated quote")
	}
	if inTok {
		tokens = append(tokens, cur.String())
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("command is empty")
	}
	return tokens, nil
}

// CheckCommand reports whether command is permitted by allowlist when run
// inside workDir. It returns nil when allowed and a descriptive error
// otherwise. Rules, in order:
//
//  1. Empty allowlist denies everything.
//  2. Shell metacharacters are rejected; the command is tokenized shell-words
//     style with no expansion.
//  3. An allowlist entry's tokens must equal the command's leading tokens
//     exactly ("go test" matches "go test ./..." but not "go testevil").
//  4. Env-var prefixes ("FOO=bar cmd") are rejected.
//  5. Exec-style flags (-exec, --exec=, -execdir, -ok, -toolexec, ...) are
//     rejected for every binary.
//  6. Path-bearing flags (-f/--file, -C/--directory, -o/--output) must point
//     inside the worktree.
//  7. Any token that is an absolute path, starts with "~", or escapes the
//     worktree via ".." is rejected. When workDir is empty, absolute paths,
//     "~" and any ".." component are rejected outright.
//  8. For go: -ldflags/-gcflags values that reach an external linker or
//     tool (-extld, -toolexec) are rejected.
func CheckCommand(command string, allowlist []string, workDir string) error {
	entries := nonEmptyEntries(allowlist)
	if len(entries) == 0 {
		return ErrEmptyAllowlist
	}
	tokens, err := TokenizeCommand(command)
	if err != nil {
		return err
	}
	if strings.Contains(tokens[0], "=") {
		return fmt.Errorf("environment variable prefix %q is not allowed", tokens[0])
	}
	if !matchesAllowlist(tokens, entries) {
		return fmt.Errorf("command %q does not match any allowlist entry", command)
	}
	return checkTokens(tokens, workDir)
}

// IsCommandAllowed is the boolean form of CheckCommand.
func IsCommandAllowed(command string, allowlist []string, workDir string) bool {
	return CheckCommand(command, allowlist, workDir) == nil
}

func nonEmptyEntries(allowlist []string) [][]string {
	var out [][]string
	for _, e := range allowlist {
		toks, err := TokenizeCommand(e)
		if err != nil {
			continue
		}
		out = append(out, toks)
	}
	return out
}

// matchesAllowlist reports whether any entry's tokens are a token-wise
// prefix of tokens.
func matchesAllowlist(tokens []string, entries [][]string) bool {
	for _, entry := range entries {
		if len(entry) > len(tokens) {
			continue
		}
		match := true
		for i, t := range entry {
			if tokens[i] != t {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// checkTokens applies the per-token safety rules (exec flags, path flags,
// path escapes, go tool injections).
func checkTokens(tokens []string, workDir string) error {
	isGo := tokens[0] == "go"
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if err := checkExecFlag(tok); err != nil {
			return err
		}
		if isGo {
			if err := checkGoFlag(tok); err != nil {
				return err
			}
		}
		if consumed, err := checkPathFlag(tokens, i, workDir); err != nil {
			return err
		} else if consumed {
			i++
			continue
		}
		if err := checkPathToken(tok, workDir); err != nil {
			return err
		}
	}
	return nil
}

func checkExecFlag(tok string) error {
	for _, f := range execFlags {
		if tok == f || strings.HasPrefix(tok, f+"=") {
			return fmt.Errorf("flag %q executes arbitrary programs and is not allowed", tok)
		}
	}
	return nil
}

// checkGoFlag rejects go flag values that reach an external tool: -ldflags /
// -gcflags / -asmflags carrying -extld/-toolexec, and -linkmode=external.
func checkGoFlag(tok string) error {
	lower := strings.ToLower(tok)
	if strings.HasPrefix(lower, "-linkmode") && strings.Contains(lower, "external") {
		return fmt.Errorf("go flag %q selects an external linker and is not allowed", tok)
	}
	for _, needle := range []string{"-extld", "-toolexec", "-extar"} {
		if strings.Contains(lower, needle) && strings.HasPrefix(lower, "-") {
			return fmt.Errorf("go flag %q reaches an external tool and is not allowed", tok)
		}
	}
	return nil
}

// checkPathFlag handles -f/-C/-o style flags. Returns consumed=true when the
// flag's value was the NEXT token (so the caller skips it).
func checkPathFlag(tokens []string, i int, workDir string) (bool, error) {
	tok := tokens[i]
	for _, f := range pathFlags {
		switch {
		case tok == f:
			if i+1 >= len(tokens) {
				return false, fmt.Errorf("flag %q is missing its path value", tok)
			}
			if err := checkPathToken(tokens[i+1], workDir); err != nil {
				return false, fmt.Errorf("flag %s: %w", f, err)
			}
			return true, nil
		case strings.HasPrefix(tok, f+"="):
			if err := checkPathToken(tok[len(f)+1:], workDir); err != nil {
				return false, fmt.Errorf("flag %s: %w", f, err)
			}
			return false, nil
		case len(f) == 2 && strings.HasPrefix(tok, f) && len(tok) > 2 && !strings.HasPrefix(tok, "--"):
			// Attached short form: -Cdir, -fMakefile.
			if err := checkPathToken(tok[2:], workDir); err != nil {
				return false, fmt.Errorf("flag %s: %w", f, err)
			}
			return false, nil
		}
	}
	return false, nil
}

// checkPathToken rejects tokens that could reference files outside workDir:
// absolute paths, "~" expansion, and ".." traversal. Tokens that are not
// path-like (flags, patterns, package specs) are examined for the same
// markers so `-run=../x` and `--dir=/etc` are also caught.
func checkPathToken(tok, workDir string) error {
	// Strip a --flag= prefix so we examine the value part.
	val := tok
	if strings.HasPrefix(val, "-") {
		if eq := strings.IndexByte(val, '='); eq >= 0 {
			val = val[eq+1:]
		} else {
			return nil // bare flag, nothing path-like
		}
	}
	if val == "" {
		return nil
	}
	if strings.HasPrefix(val, "~") {
		return fmt.Errorf("token %q uses home-directory expansion", tok)
	}
	if filepath.IsAbs(val) {
		if workDir == "" || !withinDir(val, workDir) {
			return fmt.Errorf("token %q is an absolute path outside the worktree", tok)
		}
		return nil
	}
	if hasDotDot(val) {
		if workDir == "" {
			return fmt.Errorf("token %q escapes the worktree via ..", tok)
		}
		joined := filepath.Join(workDir, val)
		if !withinDir(joined, workDir) {
			return fmt.Errorf("token %q escapes the worktree via ..", tok)
		}
	}
	return nil
}

func hasDotDot(val string) bool {
	for _, part := range strings.Split(filepath.ToSlash(val), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// withinDir reports whether path (cleaned) is dir or inside dir.
func withinDir(path, dir string) bool {
	cp := filepath.Clean(path)
	cd := filepath.Clean(dir)
	return cp == cd || strings.HasPrefix(cp, cd+string(filepath.Separator))
}
