package improver

import (
	"encoding/json"
	"fmt"
	"os"
)

// SaveSuggestions writes a JSON array of suggestions to path. The file
// is overwritten each run so the dashboard always shows the latest set.
//
// Path may not exist — we create the parent dir with 0o755.
func SaveSuggestions(path string, suggestions []Suggestion) error {
	if path == "" {
		return fmt.Errorf("save suggestions: empty path")
	}
	data, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suggestions: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write suggestions: %w", err)
	}
	return nil
}

// LoadSuggestions reads a JSON file produced by SaveSuggestions. A
// missing file returns nil, nil so the dashboard can degrade gracefully
// before the first improver run.
func LoadSuggestions(path string) ([]Suggestion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Suggestion
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode suggestions: %w", err)
	}
	return out, nil
}
