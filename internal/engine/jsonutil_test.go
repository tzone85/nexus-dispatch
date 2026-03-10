package engine

import (
	"encoding/json"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain JSON array",
			input: `[{"id":"s-001"}]`,
			want:  `[{"id":"s-001"}]`,
		},
		{
			name:  "fenced with json tag",
			input: "```json\n[{\"id\":\"s-001\"}]\n```",
			want:  `[{"id":"s-001"}]`,
		},
		{
			name:  "fenced without tag",
			input: "```\n{\"passed\":true}\n```",
			want:  `{"passed":true}`,
		},
		{
			name:  "with leading whitespace",
			input: "  \n```json\n[]\n```\n  ",
			want:  `[]`,
		},
		{
			name:  "plain JSON object",
			input: `{"on_track":true,"concerns":[],"reprioritize":[]}`,
			want:  `{"on_track":true,"concerns":[],"reprioritize":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlexibleString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "string value",
			input: `{"acceptance_criteria": "A done"}`,
			want:  "A done",
		},
		{
			name:  "array value",
			input: `{"acceptance_criteria": ["A done", "B done"]}`,
			want:  "A done\nB done",
		},
		{
			name:  "single item array",
			input: `{"acceptance_criteria": ["Only one"]}`,
			want:  "Only one",
		},
	}

	type wrapper struct {
		AC FlexibleString `json:"acceptance_criteria"`
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrapper
			if err := json.Unmarshal([]byte(tt.input), &w); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if string(w.AC) != tt.want {
				t.Errorf("got %q, want %q", string(w.AC), tt.want)
			}
		})
	}
}
