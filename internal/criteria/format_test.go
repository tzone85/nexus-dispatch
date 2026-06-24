package criteria

import (
	"reflect"
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "empty string yields no items",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace only yields no items",
			raw:  "   \n\t  ",
			want: nil,
		},
		{
			name: "single sentence is one item",
			raw:  "go test green",
			want: []string{"go test green"},
		},
		{
			name: "run-on technical sentences split into items",
			raw:  "Failing tests written first. go test green. Domain classes have no dependency on encoding/json. WorldState.copy() produces independent instance.",
			want: []string{
				"Failing tests written first.",
				"go test green.",
				"Domain classes have no dependency on encoding/json.",
				"WorldState.copy() produces independent instance.",
			},
		},
		{
			name: "method-call periods do not split",
			raw:  "WorldState.copy() produces independent instance. go test green.",
			want: []string{
				"WorldState.copy() produces independent instance.",
				"go test green.",
			},
		},
		{
			name: "abbreviation e.g. does not split sentence",
			raw:  "Use a serializer e.g. encoding/json for JSON. All tests pass.",
			want: []string{
				"Use a serializer e.g. encoding/json for JSON.",
				"All tests pass.",
			},
		},
		{
			name: "version numbers do not split",
			raw:  "go.mod declares go 1.26, cobra, pgx. Build succeeds.",
			want: []string{
				"go.mod declares go 1.26, cobra, pgx.",
				"Build succeeds.",
			},
		},
		{
			name: "existing dash bullets are preserved and stripped",
			raw:  "- First criterion\n- Second criterion\n- Third criterion",
			want: []string{"First criterion", "Second criterion", "Third criterion"},
		},
		{
			name: "numbered list items are stripped of markers",
			raw:  "1. First criterion\n2. Second criterion\n3. Third criterion",
			want: []string{"First criterion", "Second criterion", "Third criterion"},
		},
		{
			name: "newline separated lines are split even without markers",
			raw:  "Tests written first\nAll tests pass\nNo lint errors",
			want: []string{"Tests written first", "All tests pass", "No lint errors"},
		},
		{
			name: "blank lines between bullets are dropped",
			raw:  "- First\n\n- Second\n",
			want: []string{"First", "Second"},
		},
		{
			name: "bullet glyph markers are stripped",
			raw:  "• First\n• Second",
			want: []string{"First", "Second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Format(%q)\n got = %#v\nwant = %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFormatMarkdown(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "empty yields empty string",
			raw:  "",
			want: "",
		},
		{
			name: "items rendered as dash bullets",
			raw:  "First criterion. Second criterion.",
			want: "- First criterion.\n- Second criterion.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatMarkdown(tt.raw); got != tt.want {
				t.Errorf("FormatMarkdown(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
