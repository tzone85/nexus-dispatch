package update

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestPrintReport_OllamaUpdateAvailable verifies the formatted block
// when an Ollama model has a remote digest that differs from local.
// The block must include both digests (truncated), the update command,
// and a "Next auto-check" line so operators know the cadence.
func TestPrintReport_OllamaUpdateAvailable(t *testing.T) {
	res := CheckResult{
		CheckedAt: time.Now().Add(-30 * time.Minute),
		Models: []ModelStatus{
			{
				Name: "gemma4:e4b", Source: "ollama",
				LocalDigest:     "sha256:abc123def4567890ffff",
				RemoteDigest:    "sha256:9988776655443322eeee",
				UpdateAvailable: true,
				UpdateCommand:   "ollama pull gemma4:e4b",
			},
		},
	}

	var buf bytes.Buffer
	PrintReport(&buf, res, 48)

	out := buf.String()
	for _, want := range []string{
		"Checking Ollama registry",
		"gemma4:e4b",
		"update available",
		"abc123def456",          // local digest, truncated to 12
		"998877665544",          // remote digest, truncated to 12
		"ollama pull gemma4:e4b",
		"Next auto-check: in 48 hours",
		"Last checked:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintReport output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintReport_GoogleAIUpdateAvailable mirrors the Ollama branch but
// for the Google AI source. Google models report version strings rather
// than digests; the output must use that vocabulary.
func TestPrintReport_GoogleAIUpdateAvailable(t *testing.T) {
	res := CheckResult{
		CheckedAt: time.Now().Add(-2 * time.Hour),
		Models: []ModelStatus{
			{
				Name: "gemini-2.5-pro", Source: "google_ai",
				CurrentVersion:  "2.5.0",
				LatestVersion:   "2.6.0",
				UpdateAvailable: true,
				UpdateCommand:   "(no action: managed by provider)",
			},
		},
	}

	var buf bytes.Buffer
	PrintReport(&buf, res, 24)

	out := buf.String()
	for _, want := range []string{
		"Checking Google AI Studio",
		"gemini-2.5-pro",
		"update available",
		"2.5.0",
		"2.6.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintReport output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintReport_UpToDateBranches exercises the alternative format
// shown when no update is available. Both Ollama (digest only) and
// Google AI ("up to date") must render the up-to-date variant.
func TestPrintReport_UpToDateBranches(t *testing.T) {
	res := CheckResult{
		CheckedAt: time.Now(),
		Models: []ModelStatus{
			{Name: "gemma4:e4b", Source: "ollama", LocalDigest: "sha256:cafebabedeadbeef", UpdateAvailable: false},
			{Name: "gemini-2.5-pro", Source: "google_ai", UpdateAvailable: false},
		},
	}
	var buf bytes.Buffer
	PrintReport(&buf, res, 24)

	out := buf.String()
	if !strings.Contains(out, "up to date (cafebabedead)") {
		t.Errorf("ollama up-to-date line missing: %s", out)
	}
	// Don't assert exact column padding — just that the gemini line
	// rendered the up-to-date variant somewhere in the Google block.
	googleSection := strings.SplitN(out, "Checking Google AI Studio", 2)
	if len(googleSection) != 2 || !strings.Contains(googleSection[1], "gemini-2.5-pro") || !strings.Contains(googleSection[1], "up to date") {
		t.Errorf("google up-to-date line missing: %s", out)
	}
}

// TestPrintReport_NoModelsHintsOffline matches the "(are you offline?)"
// hint when CheckResult has no models. Useful for operators on a
// disconnected machine — the report should not silently render an
// empty section.
func TestPrintReport_NoModelsHintsOffline(t *testing.T) {
	var buf bytes.Buffer
	PrintReport(&buf, CheckResult{}, 24)

	out := buf.String()
	if !strings.Contains(out, "No models to check (are you offline?)") {
		t.Errorf("offline hint missing: %s", out)
	}
	if !strings.Contains(out, "Last checked: never") {
		t.Errorf("never-checked line missing: %s", out)
	}
}

// TestFilterBySource_OnlyMatchingSource sanity-checks the filter that
// PrintReport uses to split the report into Ollama / Google sections.
func TestFilterBySource_OnlyMatchingSource(t *testing.T) {
	models := []ModelStatus{
		{Name: "a", Source: "ollama"},
		{Name: "b", Source: "google_ai"},
		{Name: "c", Source: "ollama"},
	}
	got := filterBySource(models, "ollama")
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("filterBySource ollama = %+v, want [a c]", got)
	}
	if filterBySource(models, "missing") != nil {
		t.Errorf("filter missing source should return nil slice")
	}
}

// TestTruncateDigest_VariantInputs covers the corner cases of the
// 12-char truncation: short input passes through, sha256: prefix is
// stripped, longer-than-12 tail is cut.
func TestTruncateDigest_VariantInputs(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"sha256:abcdef0123456789aaaa", "abcdef012345"},
		{"abcd", "abcd"},
		{"sha256:short", "short"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := truncateDigest(tc.in); got != tc.want {
			t.Errorf("truncateDigest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
