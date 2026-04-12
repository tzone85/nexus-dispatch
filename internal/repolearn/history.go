package repolearn

import (
	"bufio"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ScanHistory performs Pass 2: git history analysis.
// It parses commit logs to detect contributor count, commit conventions,
// branch naming patterns, and churn hotspots (most-changed files).
// Requires the repository to be a valid git repo.
func ScanHistory(profile *RepoProfile) error {
	repoPath := profile.RepoPath

	// Verify this is a git repo
	if err := gitCheck(repoPath); err != nil {
		return err
	}

	// Commit count and contributor count
	profile.Conventions.CommitCount = gitCommitCount(repoPath)
	profile.Conventions.ContributorCount = gitContributorCount(repoPath)
	profile.Conventions.ActiveDays = gitActiveDays(repoPath)

	// Commit message format detection
	profile.Conventions.CommitFormat = detectCommitFormat(repoPath)

	// Branch naming convention
	profile.Conventions.BranchPattern = detectBranchPattern(repoPath)

	// Churn hotspots — top 10 most-changed files
	profile.Conventions.ChurnHotspots = computeChurnHotspots(repoPath, 10)

	// Detect stale repo signal
	lastCommitDays := daysSinceLastCommit(repoPath)
	if lastCommitDays > 180 {
		profile.AddSignal("stale", "Last commit was over 6 months ago — repo may be dormant", "")
	}

	// Detect single-contributor signal
	if profile.Conventions.ContributorCount == 1 {
		profile.AddSignal("solo_project", "Single contributor — all code written by one person", "")
	}

	profile.MarkPass(2)
	return nil
}

// gitCheck verifies the directory is inside a git repository.
func gitCheck(repoPath string) error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = repoPath
	return cmd.Run()
}

// gitCommitCount returns the total number of commits in the repository.
func gitCommitCount(repoPath string) int {
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return count
}

// gitContributorCount returns the number of unique commit authors.
func gitContributorCount(repoPath string) int {
	cmd := exec.Command("git", "shortlog", "-sn", "--all", "--no-merges")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			count++
		}
	}
	return count
}

// gitActiveDays returns the number of unique dates with at least one commit.
func gitActiveDays(repoPath string) int {
	cmd := exec.Command("git", "log", "--format=%ad", "--date=short")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	dates := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		date := strings.TrimSpace(scanner.Text())
		if date != "" {
			dates[date] = true
		}
	}
	return len(dates)
}

// conventionalRe matches Conventional Commits format: type(scope): description
var conventionalRe = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|chore|build|ci|revert)(\([^)]+\))?:\s`)

// ticketPrefixRe matches ticket-prefixed commits: PROJ-123 description
var ticketPrefixRe = regexp.MustCompile(`^[A-Z]+-\d+\s`)

// detectCommitFormat analyses the last 50 commit messages to determine the
// predominant commit message style: "conventional", "ticket-prefix", or "freeform".
func detectCommitFormat(repoPath string) string {
	cmd := exec.Command("git", "log", "--format=%s", "-50", "--no-merges")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "freeform"
	}

	conventional := 0
	ticketPrefix := 0
	total := 0

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		msg := scanner.Text()
		if msg == "" {
			continue
		}
		total++
		if conventionalRe.MatchString(msg) {
			conventional++
		} else if ticketPrefixRe.MatchString(msg) {
			ticketPrefix++
		}
	}

	if total == 0 {
		return "freeform"
	}

	// If >60% match a pattern, declare it the convention
	conventionalRatio := float64(conventional) / float64(total)
	ticketRatio := float64(ticketPrefix) / float64(total)

	if conventionalRatio > 0.6 {
		return "conventional"
	}
	if ticketRatio > 0.6 {
		return "ticket-prefix"
	}
	return "freeform"
}

// detectBranchPattern analyses remote branch names to detect naming conventions.
// Returns a pattern string like "feature/*, fix/*" or "main-only" or "freeform".
func detectBranchPattern(repoPath string) string {
	cmd := exec.Command("git", "branch", "-r", "--format=%(refname:short)")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		// Fallback: try local branches
		cmd = exec.Command("git", "branch", "--format=%(refname:short)")
		cmd.Dir = repoPath
		out, err = cmd.Output()
		if err != nil {
			return ""
		}
	}

	prefixCounts := make(map[string]int)
	total := 0

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		branch := strings.TrimSpace(scanner.Text())
		// Strip remote prefix (origin/)
		if idx := strings.Index(branch, "/"); idx >= 0 {
			rest := branch[idx+1:]
			// Check for pattern prefix like feature/, fix/, etc.
			if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
				prefix := rest[:slashIdx]
				prefixCounts[prefix]++
			}
		}
		total++
	}

	if total <= 1 {
		return "main-only"
	}

	// Build pattern from significant prefixes (>= 2 occurrences)
	var patterns []string
	for prefix, count := range prefixCounts {
		if count >= 2 {
			patterns = append(patterns, prefix+"/*")
		}
	}
	sort.Strings(patterns)

	if len(patterns) > 0 {
		return strings.Join(patterns, ", ")
	}
	return "freeform"
}

// computeChurnHotspots finds the most frequently modified files in git history.
func computeChurnHotspots(repoPath string, limit int) []ChurnHotspot {
	// git log --name-only gives us one filename per commit-file combination
	cmd := exec.Command("git", "log", "--name-only", "--format=", "--no-merges", "-500")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	fileCounts := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		file := strings.TrimSpace(scanner.Text())
		if file == "" {
			continue
		}
		// Skip non-source files
		if strings.HasPrefix(file, ".") || file == "go.sum" || file == "package-lock.json" || file == "yarn.lock" {
			continue
		}
		fileCounts[file]++
	}

	// Sort by count descending
	type fileCount struct {
		path  string
		count int
	}
	var sorted []fileCount
	for path, count := range fileCounts {
		sorted = append(sorted, fileCount{path, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	// Take top N
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	hotspots := make([]ChurnHotspot, len(sorted))
	for i, fc := range sorted {
		hotspots[i] = ChurnHotspot{Path: fc.path, Changes: fc.count}
	}
	return hotspots
}

// daysSinceLastCommit returns the number of days since the most recent commit.
func daysSinceLastCommit(repoPath string) int {
	cmd := exec.Command("git", "log", "-1", "--format=%aI")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	dateStr := strings.TrimSpace(string(out))
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return 0
	}
	return int(time.Since(t).Hours() / 24)
}
