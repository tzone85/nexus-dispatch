package update

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// PrintNotices writes a one-line "[update]" message per model that has an
// available update. Returns the number of notices printed.
func PrintNotices(w io.Writer, result CheckResult) int {
	updates := UpdatesAvailable(result)
	for _, m := range updates {
		fmt.Fprintf(w, "[update] %s has a newer version available. Run: %s\n", m.Name, m.UpdateCommand)
	}
	return len(updates)
}

// PrintReport writes a detailed, human-readable report of all checked models,
// grouped by source, followed by timing information.
func PrintReport(w io.Writer, result CheckResult, intervalHours int) {
	ollama := filterBySource(result.Models, "ollama")
	google := filterBySource(result.Models, "google_ai")

	if len(ollama) > 0 {
		fmt.Fprintf(w, "Checking Ollama registry...\n")
		for _, m := range ollama {
			if m.UpdateAvailable {
				fmt.Fprintf(w, "  %-24s update available\n", m.Name)
				fmt.Fprintf(w, "  %-24s   Local:  %s\n", "", truncateDigest(m.LocalDigest))
				fmt.Fprintf(w, "  %-24s   Remote: %s\n", "", truncateDigest(m.RemoteDigest))
				fmt.Fprintf(w, "  %-24s   Run:    %s\n", "", m.UpdateCommand)
			} else {
				fmt.Fprintf(w, "  %-24s up to date (%s)\n", m.Name, truncateDigest(m.LocalDigest))
			}
		}
		fmt.Fprintln(w)
	}

	if len(google) > 0 {
		fmt.Fprintf(w, "Checking Google AI Studio...\n")
		for _, m := range google {
			if m.UpdateAvailable {
				fmt.Fprintf(w, "  %-24s update available\n", m.Name)
				fmt.Fprintf(w, "  %-24s   Current: %s\n", "", m.CurrentVersion)
				fmt.Fprintf(w, "  %-24s   Latest:  %s\n", "", m.LatestVersion)
				fmt.Fprintf(w, "  %-24s   Run:     %s\n", "", m.UpdateCommand)
			} else {
				fmt.Fprintf(w, "  %-24s up to date\n", m.Name)
			}
		}
		fmt.Fprintln(w)
	}

	if len(result.Models) == 0 {
		fmt.Fprintf(w, "No models to check (are you offline?)\n\n")
	}

	if !result.CheckedAt.IsZero() {
		ago := time.Since(result.CheckedAt).Round(time.Second)
		fmt.Fprintf(w, "Last checked: %s ago\n", ago)
	} else {
		fmt.Fprintf(w, "Last checked: never\n")
	}
	fmt.Fprintf(w, "Next auto-check: in %d hours\n", intervalHours)
}

// filterBySource returns only the models matching the given source string.
func filterBySource(models []ModelStatus, source string) []ModelStatus {
	var filtered []ModelStatus
	for _, m := range models {
		if m.Source == source {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// truncateDigest strips a "sha256:" prefix and returns at most the first 12 characters.
func truncateDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
