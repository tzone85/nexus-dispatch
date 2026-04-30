package runtime

import "testing"

func TestLooksLikeFile(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		// Real files (have extension).
		{"main.go", true},
		{"internal/game/board.go", true},
		{"src/api/handler.ts", true},
		{"package.json", true},
		// Well-known extensionless files.
		{"Makefile", true},
		{"path/to/Dockerfile", true},
		{"Procfile", true},
		{"LICENSE", true},
		{".gitignore", true},
		{".env", true},
		// Directory-looking paths (the LB12 trap).
		{"internal/game", false},
		{"cmd/tictactoe", false},
		{"src", false},
		{"foo/bar/baz", false},
	} {
		if got := looksLikeFile(tc.path); got != tc.want {
			t.Errorf("looksLikeFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
