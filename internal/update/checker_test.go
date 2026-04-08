package update

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newOllamaMocks creates a local and remote mock server for Ollama tests.
// The local server responds to POST /api/show with the given digest.
// The remote server responds to GET /v2/library/{name}/manifests/{tag} with the given digest.
func newOllamaMocks(t *testing.T, localDigest, remoteDigest string) (local *httptest.Server, remote *httptest.Server) {
	t.Helper()

	local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{"digest": localDigest}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(local.Close)

	remote = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{"digest": remoteDigest}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(remote.Close)

	return local, remote
}

func TestCheckOllama_UpdateAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	local, remote := newOllamaMocks(t, "sha256:localdigest111", "sha256:remotedigest222")

	c := NewChecker(
		WithOllamaLocalURL(local.URL),
		WithOllamaRegistryURL(remote.URL),
	)

	results, err := c.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("CheckOllama error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	m := results[0]
	if m.Name != "gemma4:26b" {
		t.Errorf("Name = %q, want %q", m.Name, "gemma4:26b")
	}
	if m.Source != "ollama" {
		t.Errorf("Source = %q, want %q", m.Source, "ollama")
	}
	if m.LocalDigest != "sha256:localdigest111" {
		t.Errorf("LocalDigest = %q", m.LocalDigest)
	}
	if m.RemoteDigest != "sha256:remotedigest222" {
		t.Errorf("RemoteDigest = %q", m.RemoteDigest)
	}
	if !m.UpdateAvailable {
		t.Error("expected UpdateAvailable=true")
	}
	if m.UpdateCommand != "ollama pull gemma4:26b" {
		t.Errorf("UpdateCommand = %q", m.UpdateCommand)
	}
}

func TestCheckOllama_UpToDate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	digest := "sha256:samedigest999"
	local, remote := newOllamaMocks(t, digest, digest)

	c := NewChecker(
		WithOllamaLocalURL(local.URL),
		WithOllamaRegistryURL(remote.URL),
	)

	results, err := c.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("CheckOllama error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].UpdateAvailable {
		t.Error("expected UpdateAvailable=false when digests match")
	}
}

func TestCheckOllama_LocalOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Point local URL to an address that will refuse connections.
	c := NewChecker(
		WithOllamaLocalURL("http://localhost:1"),
	)

	results, err := c.CheckOllama(ctx, []string{"gemma4:26b"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when offline, got %d", len(results))
	}
}

func TestCheckOllama_Deduplicates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var localCalls atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localCalls.Add(1)
		resp := map[string]string{"digest": "sha256:abc"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(local.Close)

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{"digest": "sha256:abc"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(remote.Close)

	c := NewChecker(
		WithOllamaLocalURL(local.URL),
		WithOllamaRegistryURL(remote.URL),
	)

	results, err := c.CheckOllama(ctx, []string{"gemma4:26b", "gemma4:26b", "gemma4:26b"})
	if err != nil {
		t.Fatalf("CheckOllama error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after dedup, got %d", len(results))
	}
	if calls := localCalls.Load(); calls != 1 {
		t.Errorf("expected 1 local API call, got %d", calls)
	}
}

func TestCheckGoogleAI_NewerVersionAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"models": []map[string]string{
				{"name": "models/gemma-4-26b-a4b-it"},
				{"name": "models/gemma-4-26b-a4b-it-002"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(googleServer.Close)

	c := NewChecker(
		WithGoogleAIBaseURL(googleServer.URL),
		WithGoogleAPIKey("test-key-123"),
	)

	results, err := c.CheckGoogleAI(ctx, []string{"gemma-4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("CheckGoogleAI error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	m := results[0]
	if m.Source != "google_ai" {
		t.Errorf("Source = %q, want %q", m.Source, "google_ai")
	}
	if !m.UpdateAvailable {
		t.Error("expected UpdateAvailable=true when newer version exists")
	}
	if m.LatestVersion != "gemma-4-26b-a4b-it-002" {
		t.Errorf("LatestVersion = %q, want %q", m.LatestVersion, "gemma-4-26b-a4b-it-002")
	}
	if !strings.Contains(m.UpdateCommand, "gemma-4-26b-a4b-it-002") {
		t.Errorf("UpdateCommand = %q, should mention latest version", m.UpdateCommand)
	}
}

func TestCheckGoogleAI_UpToDate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"models": []map[string]string{
				{"name": "models/gemma-4-26b-a4b-it"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(googleServer.Close)

	c := NewChecker(
		WithGoogleAIBaseURL(googleServer.URL),
		WithGoogleAPIKey("test-key-123"),
	)

	results, err := c.CheckGoogleAI(ctx, []string{"gemma-4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("CheckGoogleAI error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].UpdateAvailable {
		t.Error("expected UpdateAvailable=false when no newer version exists")
	}
}

func TestCheckGoogleAI_NoAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewChecker() // no API key set

	results, err := c.CheckGoogleAI(ctx, []string{"gemma-4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results without API key, got %d", len(results))
	}
}

func TestRunCheck_Combined(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ollama mocks
	local, remote := newOllamaMocks(t, "sha256:aaa", "sha256:bbb")

	// Google AI mock
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"models": []map[string]string{
				{"name": "models/gemma-4-26b-a4b-it"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(googleServer.Close)

	c := NewChecker(
		WithOllamaLocalURL(local.URL),
		WithOllamaRegistryURL(remote.URL),
		WithGoogleAIBaseURL(googleServer.URL),
		WithGoogleAPIKey("test-key"),
	)

	result := c.RunCheck(ctx, []string{"gemma4:26b"}, []string{"gemma-4-26b-a4b-it"})

	if result.CheckedAt.IsZero() {
		t.Error("expected CheckedAt to be set")
	}
	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Models))
	}

	var ollamaCount, googleCount int
	for _, m := range result.Models {
		switch m.Source {
		case "ollama":
			ollamaCount++
		case "google_ai":
			googleCount++
		}
	}
	if ollamaCount != 1 {
		t.Errorf("expected 1 ollama result, got %d", ollamaCount)
	}
	if googleCount != 1 {
		t.Errorf("expected 1 google_ai result, got %d", googleCount)
	}
}

func TestPrintNotices_WithUpdates(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{
				Name:            "gemma4:26b",
				Source:          "ollama",
				UpdateAvailable: true,
				UpdateCommand:   "ollama pull gemma4:26b",
			},
			{
				Name:            "qwen3:30b",
				Source:          "ollama",
				UpdateAvailable: false,
				UpdateCommand:   "ollama pull qwen3:30b",
			},
		},
	}

	var buf bytes.Buffer
	count := PrintNotices(&buf, result)

	if count != 1 {
		t.Errorf("expected 1 notice, got %d", count)
	}

	output := buf.String()
	if !strings.Contains(output, "[update] gemma4:26b") {
		t.Errorf("expected notice for gemma4:26b, got: %s", output)
	}
	if strings.Contains(output, "qwen3:30b") {
		t.Errorf("should not mention up-to-date model, got: %s", output)
	}
	if !strings.Contains(output, "ollama pull gemma4:26b") {
		t.Errorf("expected update command in output, got: %s", output)
	}
}

func TestPrintNotices_NoUpdates(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{
				Name:            "gemma4:26b",
				Source:          "ollama",
				UpdateAvailable: false,
			},
		},
	}

	var buf bytes.Buffer
	count := PrintNotices(&buf, result)

	if count != 0 {
		t.Errorf("expected 0 notices, got %d", count)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output, got: %s", buf.String())
	}
}
