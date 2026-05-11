package git_test

import (
	"os"
	"path/filepath"
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

// TestScanRepo_AllMarkers exercises every marker branch in ScanRepo's
// switch-style detection. The existing tests cover Go, Node, TS, and
// the empty dir; the lambdas attached to Cargo/Maven/Gradle/Poetry/
// Pip stay 0% without this. Each lambda is a separate statement for
// coverage purposes — adding these brings ScanRepo above 95%.
func TestScanRepo_AllMarkers(t *testing.T) {
	cases := []struct {
		marker, lang, build string
	}{
		{"Cargo.toml", "rust", "cargo"},
		{"pom.xml", "java", "maven"},
		{"build.gradle", "java", "gradle"},
		{"pyproject.toml", "python", "poetry"},
		{"requirements.txt", "python", "pip"},
	}
	for _, tc := range cases {
		t.Run(tc.marker, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.marker), []byte(""), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			got := nxdgit.ScanRepo(dir)
			if got.Language != tc.lang {
				t.Errorf("Language = %q, want %q", got.Language, tc.lang)
			}
			if got.BuildTool != tc.build {
				t.Errorf("BuildTool = %q, want %q", got.BuildTool, tc.build)
			}
		})
	}
}
