package engine

import "testing"

func TestClampToFib(t *testing.T) {
	tests := []struct {
		input, want int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 3},
		{4, 3},
		{5, 5},
		{7, 5},
		{8, 8},
		{12, 8},
		{13, 13},
		{20, 13},
		{100, 13},
	}
	for _, tt := range tests {
		got := clampToFib(tt.input)
		if got != tt.want {
			t.Errorf("clampToFib(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestGenerateTitle(t *testing.T) {
	tests := []struct {
		index, total int
		want         string
	}{
		{0, 1, "Implement requirement"},
		{0, 3, "Setup and scaffolding"},
		{1, 3, "Implementation phase 1"},
		{2, 3, "Integration and testing"},
		{1, 2, "Integration and testing"},
	}
	for _, tt := range tests {
		got := generateTitle(tt.index, tt.total)
		if got != tt.want {
			t.Errorf("generateTitle(%d, %d) = %q, want %q", tt.index, tt.total, got, tt.want)
		}
	}
}
