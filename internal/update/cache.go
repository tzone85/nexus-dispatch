package update

import (
	"encoding/json"
	"os"
	"time"
)

// CheckResult holds the outcome of a model update check.
type CheckResult struct {
	CheckedAt time.Time     `json:"checked_at"`
	Models    []ModelStatus `json:"models"`
}

// ModelStatus describes the update state of a single model.
type ModelStatus struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	LocalDigest     string `json:"local_digest,omitempty"`
	RemoteDigest    string `json:"remote_digest,omitempty"`
	CurrentVersion  string `json:"current_version,omitempty"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateCommand   string `json:"update_command"`
}

// WriteCache writes the check result to the given path as JSON.
func WriteCache(path string, result CheckResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ReadCache reads a cached check result. Returns a zero-value CheckResult
// (with zero CheckedAt) if the file doesn't exist.
func ReadCache(path string) (CheckResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{}, nil
		}
		return CheckResult{}, err
	}
	var result CheckResult
	if err := json.Unmarshal(data, &result); err != nil {
		return CheckResult{}, err
	}
	return result, nil
}

// IsStale returns true if the cache is older than intervalHours or has never been checked.
func IsStale(result CheckResult, intervalHours int) bool {
	if result.CheckedAt.IsZero() {
		return true
	}
	return time.Since(result.CheckedAt) > time.Duration(intervalHours)*time.Hour
}

// UpdatesAvailable returns only the models with updates available.
func UpdatesAvailable(result CheckResult) []ModelStatus {
	var updates []ModelStatus
	for _, m := range result.Models {
		if m.UpdateAvailable {
			updates = append(updates, m)
		}
	}
	return updates
}
