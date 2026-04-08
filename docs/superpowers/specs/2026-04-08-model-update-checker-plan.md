# Model Update Checker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a lightweight model update checker that notifies users when newer Ollama/Google AI model versions are available, with a manual `nxd models check` command and background auto-check every 48 hours.

**Architecture:** New `internal/update/` package with checker, cache, and notification logic. CLI integration via `PersistentPreRun` hook on the root command. Config additions for opt-out. Fire-and-forget goroutines for non-blocking checks with 3-second timeouts.

**Tech Stack:** Go `net/http`, `encoding/json`, `sync/atomic`, `time`, Cobra CLI, `httptest` for testing

**Spec:** `docs/superpowers/specs/2026-04-08-model-update-checker-design.md`

---

## File Structure

```
internal/update/
├── checker.go          # CheckOllama(), CheckGoogleAI(), RunCheck()
├── checker_test.go     # Mock HTTP servers for both sources
├── cache.go            # ReadCache(), WriteCache(), IsStale()
├── cache_test.go       # Round-trip, expiry, missing file
├── notify.go           # PrintNotices(), PrintReport()
internal/cli/
├── models.go           # nxd models check command
├── root.go             # Modified: add PersistentPreRun hook
internal/config/
├── config.go           # Modified: add UpdateCheck, UpdateIntervalHours
├── loader.go           # Modified: add defaults
├── config_test.go      # Modified: test new fields
```

---

### Task 1: Cache Types and Read/Write

**Files:**
- Create: `internal/update/cache.go`
- Create: `internal/update/cache_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/update/cache_test.go
package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-status.json")

	result := CheckResult{
		CheckedAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
		Models: []ModelStatus{
			{
				Name:            "gemma4:26b",
				Source:          "ollama",
				LocalDigest:     "abc123",
				RemoteDigest:    "def456",
				UpdateAvailable: true,
				UpdateCommand:   "ollama pull gemma4:26b",
			},
		},
	}

	if err := WriteCache(path, result); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	got, err := ReadCache(path)
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}

	if got.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero")
	}
	if len(got.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(got.Models))
	}
	if got.Models[0].Name != "gemma4:26b" {
		t.Errorf("Name = %q", got.Models[0].Name)
	}
	if !got.Models[0].UpdateAvailable {
		t.Error("expected UpdateAvailable=true")
	}
}

func TestReadCache_MissingFile(t *testing.T) {
	got, err := ReadCache("/nonexistent/path/update-status.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if !got.CheckedAt.IsZero() {
		t.Error("expected zero CheckedAt for missing cache")
	}
}

func TestIsStale_Fresh(t *testing.T) {
	result := CheckResult{CheckedAt: time.Now()}
	if IsStale(result, 48) {
		t.Error("expected fresh cache to not be stale")
	}
}

func TestIsStale_Old(t *testing.T) {
	result := CheckResult{CheckedAt: time.Now().Add(-49 * time.Hour)}
	if !IsStale(result, 48) {
		t.Error("expected 49-hour-old cache to be stale")
	}
}

func TestIsStale_ZeroTime(t *testing.T) {
	result := CheckResult{}
	if !IsStale(result, 48) {
		t.Error("expected zero-time cache to be stale")
	}
}

func TestUpdatesAvailable(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{Name: "a", UpdateAvailable: false},
			{Name: "b", UpdateAvailable: true},
		},
	}
	updates := UpdatesAvailable(result)
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Name != "b" {
		t.Errorf("Name = %q", updates[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/update/ -run TestWrite -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the implementation**

```go
// internal/update/cache.go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/update/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/update/cache.go internal/update/cache_test.go
git commit -m "feat: add model update cache with read/write and staleness check"
```

---

### Task 2: Ollama Update Checker

**Files:**
- Create: `internal/update/checker.go`
- Create: `internal/update/checker_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/update/checker_test.go
package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckOllama_UpdateAvailable(t *testing.T) {
	// Mock local Ollama (api/show)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"digest": "sha256:localdigest111",
		})
	}))
	defer localServer.Close()

	// Mock remote Ollama registry
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"digest": "sha256:remotedigest222",
		})
	}))
	defer remoteServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checker := NewChecker(
		WithOllamaLocalURL(localServer.URL),
		WithOllamaRegistryURL(remoteServer.URL),
	)

	results, err := checker.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("CheckOllama: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].UpdateAvailable {
		t.Error("expected update available (digests differ)")
	}
	if results[0].UpdateCommand != "ollama pull gemma4:26b" {
		t.Errorf("UpdateCommand = %q", results[0].UpdateCommand)
	}
}

func TestCheckOllama_UpToDate(t *testing.T) {
	sameDigest := "sha256:samedigest999"
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"digest": sameDigest})
	}))
	defer localServer.Close()

	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"digest": sameDigest})
	}))
	defer remoteServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checker := NewChecker(
		WithOllamaLocalURL(localServer.URL),
		WithOllamaRegistryURL(remoteServer.URL),
	)

	results, err := checker.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("CheckOllama: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].UpdateAvailable {
		t.Error("expected no update (digests match)")
	}
}

func TestCheckOllama_LocalOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	checker := NewChecker(
		WithOllamaLocalURL("http://localhost:1"), // nothing listening
	)

	results, err := checker.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("expected no error when offline, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when offline, got %d", len(results))
	}
}

func TestCheckOllama_Deduplicates(t *testing.T) {
	callCount := 0
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]any{"digest": "sha256:abc"})
	}))
	defer localServer.Close()

	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"digest": "sha256:abc"})
	}))
	defer remoteServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checker := NewChecker(
		WithOllamaLocalURL(localServer.URL),
		WithOllamaRegistryURL(remoteServer.URL),
	)

	// Same model listed 3 times (all 7 roles use same model)
	results, _ := checker.CheckOllama(ctx, []string{"gemma4:26b", "gemma4:26b", "gemma4:26b"})

	if len(results) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(results))
	}
	if callCount != 1 {
		t.Errorf("expected 1 local API call (deduplicated), got %d", callCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/update/ -run TestCheckOllama -v`
Expected: FAIL — Checker type not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/update/checker.go
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOllamaLocalURL    = "http://localhost:11434"
	defaultOllamaRegistryURL = "https://registry.ollama.ai"
	defaultGoogleAIBaseURL   = "https://generativelanguage.googleapis.com/v1beta"
	defaultCheckTimeout      = 3 * time.Second
)

// Checker queries model sources for updates.
type Checker struct {
	ollamaLocalURL    string
	ollamaRegistryURL string
	googleAIBaseURL   string
	googleAPIKey      string
	httpClient        *http.Client
}

// CheckerOption configures a Checker.
type CheckerOption func(*Checker)

func WithOllamaLocalURL(url string) CheckerOption {
	return func(c *Checker) { c.ollamaLocalURL = url }
}

func WithOllamaRegistryURL(url string) CheckerOption {
	return func(c *Checker) { c.ollamaRegistryURL = url }
}

func WithGoogleAIBaseURL(url string) CheckerOption {
	return func(c *Checker) { c.googleAIBaseURL = url }
}

func WithGoogleAPIKey(key string) CheckerOption {
	return func(c *Checker) { c.googleAPIKey = key }
}

// NewChecker creates a checker with the given options.
func NewChecker(opts ...CheckerOption) *Checker {
	c := &Checker{
		ollamaLocalURL:    defaultOllamaLocalURL,
		ollamaRegistryURL: defaultOllamaRegistryURL,
		googleAIBaseURL:   defaultGoogleAIBaseURL,
		httpClient:        &http.Client{Timeout: defaultCheckTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// CheckOllama checks pulled models for newer versions in the Ollama registry.
// Returns only successfully checked models. Silently skips failures.
func (c *Checker) CheckOllama(ctx context.Context, models []string) ([]ModelStatus, error) {
	seen := map[string]bool{}
	var results []ModelStatus

	for _, model := range models {
		if seen[model] {
			continue
		}
		seen[model] = true

		status, err := c.checkOneOllamaModel(ctx, model)
		if err != nil {
			continue // silently skip failures
		}
		results = append(results, status)
	}

	return results, nil
}

func (c *Checker) checkOneOllamaModel(ctx context.Context, model string) (ModelStatus, error) {
	// Get local digest
	localDigest, err := c.ollamaLocalDigest(ctx, model)
	if err != nil {
		return ModelStatus{}, err
	}

	// Get remote digest
	remoteDigest, err := c.ollamaRemoteDigest(ctx, model)
	if err != nil {
		return ModelStatus{}, err
	}

	return ModelStatus{
		Name:            model,
		Source:          "ollama",
		LocalDigest:     localDigest,
		RemoteDigest:    remoteDigest,
		UpdateAvailable: localDigest != remoteDigest,
		UpdateCommand:   fmt.Sprintf("ollama pull %s", model),
	}, nil
}

func (c *Checker) ollamaLocalDigest(ctx context.Context, model string) (string, error) {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ollamaLocalURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	return result.Digest, nil
}

func (c *Checker) ollamaRemoteDigest(ctx context.Context, model string) (string, error) {
	// Parse model name into library/tag
	name, tag := parseOllamaModel(model)

	url := fmt.Sprintf("%s/v2/library/%s/manifests/%s", c.ollamaRegistryURL, name, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	return result.Digest, nil
}

// parseOllamaModel splits "gemma4:26b" into ("gemma4", "26b").
// If no tag, defaults to "latest".
func parseOllamaModel(model string) (string, string) {
	parts := strings.SplitN(model, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], "latest"
}

// CheckGoogleAI checks for newer model versions on Google AI Studio.
// Requires a valid API key. Returns empty if key is not set.
func (c *Checker) CheckGoogleAI(ctx context.Context, configuredModels []string) ([]ModelStatus, error) {
	if c.googleAPIKey == "" {
		return nil, nil
	}

	seen := map[string]bool{}
	var unique []string
	for _, m := range configuredModels {
		if !seen[m] {
			seen[m] = true
			unique = append(unique, m)
		}
	}

	url := fmt.Sprintf("%s/models?key=%s", c.googleAIBaseURL, c.googleAPIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil // silently skip
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil // silently skip (offline)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}

	var listResp struct {
		Models []struct {
			Name string `json:"name"` // format: "models/gemma-4-26b-a4b-it"
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &listResp); err != nil {
		return nil, nil
	}

	// Build set of available model names (strip "models/" prefix)
	available := map[string]bool{}
	for _, m := range listResp.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		available[name] = true
	}

	var results []ModelStatus
	for _, configured := range unique {
		// Check if a newer version exists (e.g., configured is gemma-4-26b-a4b-it, available has gemma-4-26b-a4b-it-002)
		latestVersion := findLatestVersion(configured, available)
		results = append(results, ModelStatus{
			Name:            configured,
			Source:          "google_ai",
			CurrentVersion:  configured,
			LatestVersion:   latestVersion,
			UpdateAvailable: latestVersion != configured,
			UpdateCommand:   fmt.Sprintf("Update google_model in nxd.yaml to: %s", latestVersion),
		})
	}

	return results, nil
}

// findLatestVersion looks for a versioned successor of the model name.
// E.g., if "gemma-4-26b-a4b-it" is configured and "gemma-4-26b-a4b-it-002" exists, returns the latter.
func findLatestVersion(current string, available map[string]bool) string {
	best := current
	for name := range available {
		if strings.HasPrefix(name, current) && name > best {
			best = name
		}
	}
	return best
}

// RunCheck runs all configured checks and returns a combined result.
func (c *Checker) RunCheck(ctx context.Context, ollamaModels, googleModels []string) CheckResult {
	result := CheckResult{CheckedAt: time.Now()}

	ollamaResults, _ := c.CheckOllama(ctx, ollamaModels)
	result.Models = append(result.Models, ollamaResults...)

	googleResults, _ := c.CheckGoogleAI(ctx, googleModels)
	result.Models = append(result.Models, googleResults...)

	return result
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/update/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/update/checker.go internal/update/checker_test.go
git commit -m "feat: add Ollama and Google AI model update checker"
```

---

### Task 3: Google AI Checker Tests

**Files:**
- Modify: `internal/update/checker_test.go`

- [ ] **Step 1: Add Google AI tests**

Append to `internal/update/checker_test.go`:

```go
func TestCheckGoogleAI_NewerVersionAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-key" {
			t.Error("expected API key in query")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "models/gemma-4-26b-a4b-it"},
				{"name": "models/gemma-4-26b-a4b-it-002"},
				{"name": "models/gemma-4-31b-it"},
			},
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checker := NewChecker(
		WithGoogleAIBaseURL(server.URL),
		WithGoogleAPIKey("test-key"),
	)

	results, err := checker.CheckGoogleAI(ctx, []string{"gemma-4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("CheckGoogleAI: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].UpdateAvailable {
		t.Error("expected update available (newer version exists)")
	}
	if results[0].LatestVersion != "gemma-4-26b-a4b-it-002" {
		t.Errorf("LatestVersion = %q", results[0].LatestVersion)
	}
}

func TestCheckGoogleAI_UpToDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "models/gemma-4-26b-a4b-it"},
			},
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checker := NewChecker(
		WithGoogleAIBaseURL(server.URL),
		WithGoogleAPIKey("test-key"),
	)

	results, _ := checker.CheckGoogleAI(ctx, []string{"gemma-4-26b-a4b-it"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].UpdateAvailable {
		t.Error("expected no update (same version)")
	}
}

func TestCheckGoogleAI_NoAPIKey(t *testing.T) {
	checker := NewChecker() // no API key

	results, err := checker.CheckGoogleAI(context.Background(), []string{"gemma-4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results without API key, got %d", len(results))
	}
}

func TestRunCheck_Combined(t *testing.T) {
	ollamaLocal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"digest": "sha256:abc"})
	}))
	defer ollamaLocal.Close()

	ollamaRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"digest": "sha256:xyz"})
	}))
	defer ollamaRemote.Close()

	googleAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"name": "models/gemma-4-26b-a4b-it"}},
		})
	}))
	defer googleAI.Close()

	checker := NewChecker(
		WithOllamaLocalURL(ollamaLocal.URL),
		WithOllamaRegistryURL(ollamaRemote.URL),
		WithGoogleAIBaseURL(googleAI.URL),
		WithGoogleAPIKey("test-key"),
	)

	result := checker.RunCheck(context.Background(), []string{"gemma4:26b"}, []string{"gemma-4-26b-a4b-it"})

	if result.CheckedAt.IsZero() {
		t.Error("CheckedAt should be set")
	}
	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models (1 ollama + 1 google), got %d", len(result.Models))
	}

	// Ollama model should show update (different digests)
	ollama := result.Models[0]
	if ollama.Source != "ollama" {
		t.Errorf("first result source = %q", ollama.Source)
	}
	if !ollama.UpdateAvailable {
		t.Error("expected ollama update available")
	}

	// Google model should be up to date
	google := result.Models[1]
	if google.Source != "google_ai" {
		t.Errorf("second result source = %q", google.Source)
	}
	if google.UpdateAvailable {
		t.Error("expected google model up to date")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/update/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/update/checker_test.go
git commit -m "test: add Google AI checker and RunCheck combined tests"
```

---

### Task 4: Notification Formatting

**Files:**
- Create: `internal/update/notify.go`
- Modify: `internal/update/checker_test.go` (add notify tests)

- [ ] **Step 1: Write the implementation**

```go
// internal/update/notify.go
package update

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// PrintNotices writes one-line update notices to the writer (typically os.Stderr).
// Returns the number of notices printed.
func PrintNotices(w io.Writer, result CheckResult) int {
	updates := UpdatesAvailable(result)
	for _, m := range updates {
		fmt.Fprintf(w, "[update] %s has a newer version available. Run: %s\n", m.Name, m.UpdateCommand)
	}
	return len(updates)
}

// PrintReport writes a detailed model status report to the writer.
func PrintReport(w io.Writer, result CheckResult, intervalHours int) {
	// Group by source
	ollama := filterBySource(result.Models, "ollama")
	google := filterBySource(result.Models, "google_ai")

	if len(ollama) > 0 {
		fmt.Fprintf(w, "Checking Ollama registry...\n")
		for _, m := range ollama {
			if m.UpdateAvailable {
				fmt.Fprintf(w, "  %-24s ⬆ update available\n", m.Name)
				fmt.Fprintf(w, "  %-24s   Local:  %s\n", "", truncateDigest(m.LocalDigest))
				fmt.Fprintf(w, "  %-24s   Remote: %s\n", "", truncateDigest(m.RemoteDigest))
				fmt.Fprintf(w, "  %-24s   Run:    %s\n", "", m.UpdateCommand)
			} else {
				fmt.Fprintf(w, "  %-24s ✓ up to date (%s)\n", m.Name, truncateDigest(m.LocalDigest))
			}
		}
		fmt.Fprintln(w)
	}

	if len(google) > 0 {
		fmt.Fprintf(w, "Checking Google AI Studio...\n")
		for _, m := range google {
			if m.UpdateAvailable {
				fmt.Fprintf(w, "  %-24s ⬆ update available\n", m.Name)
				fmt.Fprintf(w, "  %-24s   Current: %s\n", "", m.CurrentVersion)
				fmt.Fprintf(w, "  %-24s   Latest:  %s\n", "", m.LatestVersion)
				fmt.Fprintf(w, "  %-24s   Run:     %s\n", "", m.UpdateCommand)
			} else {
				fmt.Fprintf(w, "  %-24s ✓ up to date\n", m.Name)
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

func filterBySource(models []ModelStatus, source string) []ModelStatus {
	var filtered []ModelStatus
	for _, m := range models {
		if m.Source == source {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func truncateDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
```

- [ ] **Step 2: Add a quick test for PrintNotices**

Append to `internal/update/checker_test.go`:

```go
func TestPrintNotices_WithUpdates(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{Name: "gemma4:26b", UpdateAvailable: true, UpdateCommand: "ollama pull gemma4:26b"},
			{Name: "other:latest", UpdateAvailable: false},
		},
	}

	var buf strings.Builder
	count := PrintNotices(&buf, result)

	if count != 1 {
		t.Errorf("expected 1 notice, got %d", count)
	}
	if !strings.Contains(buf.String(), "gemma4:26b") {
		t.Error("expected model name in notice")
	}
	if !strings.Contains(buf.String(), "ollama pull") {
		t.Error("expected update command in notice")
	}
}

func TestPrintNotices_NoUpdates(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{Name: "gemma4:26b", UpdateAvailable: false},
		},
	}

	var buf strings.Builder
	count := PrintNotices(&buf, result)

	if count != 0 {
		t.Errorf("expected 0 notices, got %d", count)
	}
	if buf.Len() != 0 {
		t.Error("expected no output")
	}
}
```

Add `"strings"` to the imports in checker_test.go if not already present.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/update/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/update/notify.go internal/update/checker_test.go
git commit -m "feat: add update notification formatting (notices + detailed report)"
```

---

### Task 5: Config Changes

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add fields to WorkspaceConfig**

In `internal/config/config.go`, add to `WorkspaceConfig`:

```go
type WorkspaceConfig struct {
	StateDir            string `yaml:"state_dir"`
	Backend             string `yaml:"backend"`
	LogLevel            string `yaml:"log_level"`
	LogRetentionDays    int    `yaml:"log_retention_days"`
	UpdateCheck         bool   `yaml:"update_check"`            // NEW
	UpdateIntervalHours int    `yaml:"update_interval_hours"`   // NEW
}
```

In `Validate()`, add after existing validation:

```go
if c.Workspace.UpdateIntervalHours < 0 {
	return fmt.Errorf("workspace.update_interval_hours must be >= 0, got %d", c.Workspace.UpdateIntervalHours)
}
```

- [ ] **Step 2: Add defaults in loader.go**

In `DefaultConfig()`, update the Workspace block:

```go
Workspace: WorkspaceConfig{
	StateDir:            "~/.nxd",
	Backend:             "sqlite",
	LogLevel:            "info",
	LogRetentionDays:    30,
	UpdateCheck:         true,
	UpdateIntervalHours: 48,
},
```

- [ ] **Step 3: Add tests**

Append to `internal/config/config_test.go`:

```go
func TestDefaultConfig_UpdateCheckDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Workspace.UpdateCheck {
		t.Error("expected UpdateCheck=true by default")
	}
	if cfg.Workspace.UpdateIntervalHours != 48 {
		t.Errorf("UpdateIntervalHours = %d, want 48", cfg.Workspace.UpdateIntervalHours)
	}
}

func TestValidation_NegativeUpdateInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Workspace.UpdateIntervalHours = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative update_interval_hours")
	}
}

func TestValidation_ZeroUpdateInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Workspace.UpdateIntervalHours = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected zero interval to pass validation, got: %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/config_test.go
git commit -m "feat: add update_check and update_interval_hours config fields"
```

---

### Task 6: `nxd models check` Command

**Files:**
- Create: `internal/cli/models.go`

- [ ] **Step 1: Write the command**

```go
// internal/cli/models.go
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage and check model versions",
	}
	cmd.AddCommand(newModelsCheckCmd())
	return cmd
}

func newModelsCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check for model updates from Ollama and Google AI Studio",
		Long:  "Queries Ollama registry and Google AI Studio for newer versions of configured models. Always runs even if update_check is disabled in config.",
		RunE:  runModelsCheck,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runModelsCheck(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	cachePath := filepath.Join(stateDir, "update-status.json")
	out := cmd.OutOrStdout()

	// Collect unique model names from config
	ollamaModels, googleModels := collectConfiguredModels(cfg)

	// Build checker
	opts := []update.CheckerOption{}
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		opts = append(opts, update.WithOllamaLocalURL(host))
	}
	if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
		opts = append(opts, update.WithGoogleAPIKey(key))
	}
	checker := update.NewChecker(opts...)

	// Run synchronously
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checker.RunCheck(ctx, ollamaModels, googleModels)

	// Write cache
	if err := update.WriteCache(cachePath, result); err != nil {
		fmt.Fprintf(out, "Warning: could not write cache: %v\n", err)
	}

	// Print detailed report
	update.PrintReport(out, result, cfg.Workspace.UpdateIntervalHours)
	return nil
}

// collectConfiguredModels extracts unique Ollama and Google AI model names from config.
func collectConfiguredModels(cfg config.Config) (ollama, google []string) {
	ollamaSeen := map[string]bool{}
	googleSeen := map[string]bool{}

	for _, mc := range cfg.Models.All() {
		if mc.Model != "" && strings.Contains(mc.Provider, "ollama") {
			if !ollamaSeen[mc.Model] {
				ollamaSeen[mc.Model] = true
				ollama = append(ollama, mc.Model)
			}
		}
		if mc.GoogleModel != "" && strings.Contains(mc.Provider, "google") {
			if !googleSeen[mc.GoogleModel] {
				googleSeen[mc.GoogleModel] = true
				google = append(google, mc.GoogleModel)
			}
		}
	}
	return
}
```

Add the missing import for `config`:

```go
import (
	"github.com/tzone85/nexus-dispatch/internal/config"
)
```

- [ ] **Step 2: Register the command in root.go**

In `internal/cli/root.go`, add to the `init()` function:

```go
rootCmd.AddCommand(newModelsCmd())
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./cmd/nxd/`
Expected: Success

- [ ] **Step 4: Test manually**

Run: `./nxd models check`
Expected: Output showing Ollama model status (may show "No models to check" if running from a directory without nxd.yaml)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/models.go internal/cli/root.go
git commit -m "feat: add nxd models check command"
```

---

### Task 7: PersistentPreRun Hook (Background Auto-Check)

**Files:**
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Add the background check hook**

Update `internal/cli/root.go` to add a `PersistentPreRun` function:

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

var version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:     "nxd",
	Short:   "Nexus Dispatch -- AI agent orchestrator",
	Long:    "NXD orchestrates autonomous AI agents through the full software development lifecycle.\nHand off a requirement, walk away, come back to merged PRs.",
	Version: version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		checkForModelUpdates(cmd)
	},
}

func init() {
	rootCmd.PersistentFlags().String("config", "nxd.yaml", "Path to config file")

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newReqCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newPauseCmd())
	rootCmd.AddCommand(newResumeCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newEscalationsCmd())
	rootCmd.AddCommand(newGCCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newEventsCmd())
	rootCmd.AddCommand(newDashboardCmd())
	rootCmd.AddCommand(newArchiveCmd())
	rootCmd.AddCommand(newModelsCmd())
}

func Execute() error {
	return rootCmd.Execute()
}

// checkForModelUpdates runs a non-blocking background check for model updates.
// Reads cached results and prints notices to stderr. If cache is stale, spawns
// a fire-and-forget goroutine to refresh it.
func checkForModelUpdates(cmd *cobra.Command) {
	// Check env var override
	if os.Getenv("NXD_UPDATE_CHECK") == "false" {
		return
	}

	// Try to load config (silently skip if config not available)
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return // no config = skip update check silently
	}

	// Check opt-out
	if !cfg.Workspace.UpdateCheck {
		return
	}
	interval := cfg.Workspace.UpdateIntervalHours
	if interval <= 0 {
		return
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	cachePath := filepath.Join(stateDir, "update-status.json")

	// Read cached results
	cached, err := update.ReadCache(cachePath)
	if err != nil {
		return
	}

	// Print notices from cache (if any)
	if len(update.UpdatesAvailable(cached)) > 0 {
		update.PrintNotices(os.Stderr, cached)
	}

	// If cache is stale, spawn background refresh
	if update.IsStale(cached, interval) {
		go func() {
			ollamaModels, googleModels := collectConfiguredModels(cfg)

			opts := []update.CheckerOption{}
			if host := os.Getenv("OLLAMA_HOST"); host != "" {
				opts = append(opts, update.WithOllamaLocalURL(host))
			}
			if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
				opts = append(opts, update.WithGoogleAPIKey(key))
			}
			checker := update.NewChecker(opts...)

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()

			result := checker.RunCheck(ctx, ollamaModels, googleModels)
			update.WriteCache(cachePath, result)
		}()
	}
}
```

**IMPORTANT:** The `collectConfiguredModels` function is defined in `models.go` (Task 6). Both files are in the `cli` package so it's accessible.

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/nxd/`
Expected: Success

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/cli/root.go
git commit -m "feat: add background model update check on every CLI command"
```

---

### Task 8: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS (all packages)

- [ ] **Step 2: Run update package tests with race detection**

Run: `go test ./internal/update/ -race -v`
Expected: PASS

- [ ] **Step 3: Build and test CLI**

Run:
```bash
go build -o /tmp/nxd ./cmd/nxd/
/tmp/nxd --help
/tmp/nxd models check
```
Expected: Help shows `models` command. `models check` runs and produces output.

- [ ] **Step 4: Test opt-out**

Run: `NXD_UPDATE_CHECK=false /tmp/nxd status`
Expected: No `[update]` notices (even if cache has updates)

- [ ] **Step 5: Verify config includes new defaults**

Run:
```bash
cd /tmp && rm -f nxd.yaml && /tmp/nxd init && grep -A2 "update" nxd.yaml
```
Expected: Shows `update_check: true` and `update_interval_hours: 48`
