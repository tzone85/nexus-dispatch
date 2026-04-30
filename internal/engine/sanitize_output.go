package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// VXD Phase 1.1, 1.2, 1.4 ports.
//
// captureFileTree, hallucinationLinePatterns, scrubFile, validateBuild are
// adapted from the VXD sibling at ~/Sites/misc/vortex-dispatch/. The patterns
// list was tuned over ~2 months of production use against Claude / Sonnet
// outputs; we keep them verbatim because small-model (Gemma) preambles look
// nearly identical.

// captureFileTree returns `git ls-files` output for the given worktree.
// Empty string on error — the caller treats that as "no extra context".
func captureFileTree(worktreePath string) string {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	const maxBytes = 16 * 1024 // cap context bloat for huge repos
	if len(out) > maxBytes {
		out = append(out[:maxBytes], []byte("\n... (truncated)")...)
	}
	return strings.TrimSpace(string(out))
}

// hallucinationLinePatterns are LLM-generated preamble fragments commonly
// emitted as the first lines of a code file. Matching is case-insensitive
// prefix on a trimmed line.
var hallucinationLinePatterns = []string{
	"looking at",
	"i'll ",
	"i will ",
	"here's ",
	"here is ",
	"based on",
	"i notice",
	"let me ",
	"this code",
	"the code",
	"this file",
	"the file",
	"as you can see",
	"to fix this",
	"the change",
	"the fix",
	"the implementation",
	"to implement",
	"i'm going to",
	"i've ",
	"i have ",
	"first, ",
	"now i ",
	"now let",
	"sure, ",
	"certainly,",
	"of course",
}

// isHallucinationLine reports whether s looks like LLM reasoning preamble
// rather than source code. Empty / whitespace lines never match.
func isHallucinationLine(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return false
	}
	for _, p := range hallucinationLinePatterns {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// isSourceExt reports whether the given path has a file extension we treat
// as source code (subject to scrubbing). Markdown / log / data files are
// intentionally NOT scrubbed because legitimate prose often matches the
// preamble patterns.
func isSourceExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".java", ".kt",
		".rb", ".rs", ".c", ".cpp", ".cc", ".h", ".hpp", ".cs",
		".php", ".swift", ".scala", ".sh", ".bash":
		return true
	}
	return false
}

// scrubFile rewrites path with leading hallucination preamble lines removed.
// Returns (scrubbed, removedLineCount, error). When the file consists entirely
// of hallucination lines, the file is left untouched and a warning is logged
// — better to keep agent output for inspection than silently empty a file.
func scrubFile(path string) (bool, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, 0, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return false, 0, err
	}

	// Scrub leading preamble only — once we hit a non-preamble line, stop.
	stripIdx := 0
	for stripIdx < len(lines) {
		l := strings.TrimSpace(lines[stripIdx])
		if l == "" || isHallucinationLine(lines[stripIdx]) {
			stripIdx++
			continue
		}
		break
	}
	if stripIdx == 0 {
		return false, 0, nil
	}
	if stripIdx == len(lines) {
		log.Printf("[scrub] %s appears to be entirely LLM reasoning — leaving in place for inspection", path)
		return false, 0, nil
	}

	scrubbed := strings.Join(lines[stripIdx:], "\n")
	if !strings.HasSuffix(scrubbed, "\n") {
		scrubbed += "\n"
	}
	if err := os.WriteFile(path, []byte(scrubbed), 0o644); err != nil {
		return false, 0, err
	}
	return true, stripIdx, nil
}

// scrubHallucinationsFromWorktree walks the worktree, runs scrubFile on each
// source file under it, and returns (filesScrubbed, totalLinesRemoved). It is
// intended to be called immediately after autoCommit and before gitDiff so
// the diff sent to the reviewer reflects the cleaned files.
func scrubHallucinationsFromWorktree(worktreePath string) (int, int) {
	files := 0
	lines := 0
	_ = filepath.Walk(worktreePath, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(p, ".git/") {
			return nil
		}
		if !isSourceExt(p) {
			return nil
		}
		changed, removed, scrubErr := scrubFile(p)
		if scrubErr != nil {
			log.Printf("[scrub] %s: %v", p, scrubErr)
			return nil
		}
		if changed {
			files++
			lines += removed
			log.Printf("[scrub] removed %d preamble line(s) from %s", removed, p)
		}
		return nil
	})
	return files, lines
}

// scanFileForConflictMarkers returns true if path contains an unresolved
// merge conflict marker (`<<<<<<<`).
func scanFileForConflictMarkers(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		l := scanner.Text()
		if strings.HasPrefix(l, "<<<<<<<") ||
			strings.HasPrefix(l, "=======") && strings.TrimSpace(l) == "=======" ||
			strings.HasPrefix(l, ">>>>>>>") {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// validateNoConflictMarkers walks the worktree and returns a list of files
// that still contain merge conflict markers. Empty slice on success.
func validateNoConflictMarkers(worktreePath string) []string {
	var bad []string
	_ = filepath.Walk(worktreePath, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(p, ".git/") {
			return nil
		}
		if !isSourceExt(p) {
			return nil
		}
		dirty, scanErr := scanFileForConflictMarkers(p)
		if scanErr != nil {
			return nil
		}
		if dirty {
			bad = append(bad, p)
		}
		return nil
	})
	return bad
}

// validateBuild runs a project-type-specific build check in worktreePath
// and returns nil on success or an error describing the first failure
// observed (output truncated to 4 KB). Returns nil + log message if no
// known project type is detected.
//
// VXD Phase 1.2: this is a non-blocking signal. Callers log the failure
// rather than aborting the pipeline, because the reviewer / QA stage will
// still flag the broken build and a hint is more useful than a blocked
// merge for stories that intentionally split refactor work.
func validateBuild(ctx context.Context, worktreePath string) error {
	type check struct {
		marker string
		name   string
		bin    string
		args   []string
	}
	checks := []check{
		{"go.mod", "go", "go", []string{"build", "./..."}},
		{"package.json", "npm", "npm", []string{"run", "build", "--if-present"}},
		{"pyproject.toml", "python", "python3", []string{"-m", "py_compile"}}, // weak but cheap
		{"Cargo.toml", "cargo", "cargo", []string{"build"}},
	}

	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(worktreePath, c.marker)); err != nil {
			continue
		}

		buildCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(buildCtx, c.bin, c.args...)
		cmd.Dir = worktreePath
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			truncated := out.String()
			if len(truncated) > 4096 {
				truncated = truncated[:4096] + "\n... (truncated)"
			}
			return fmt.Errorf("%s build failed: %w\n%s", c.name, err, truncated)
		}
		return nil
	}

	log.Printf("[build-validate] no recognized project marker in %s — skipping", worktreePath)
	return nil
}
