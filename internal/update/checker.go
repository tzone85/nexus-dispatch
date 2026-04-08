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

// Checker queries Ollama and Google AI endpoints for model update information.
type Checker struct {
	ollamaLocalURL    string
	ollamaRegistryURL string
	googleAIBaseURL   string
	googleAPIKey      string
	httpClient        *http.Client
}

// CheckerOption configures a Checker.
type CheckerOption func(*Checker)

// WithOllamaLocalURL overrides the local Ollama API URL.
func WithOllamaLocalURL(url string) CheckerOption {
	return func(c *Checker) { c.ollamaLocalURL = url }
}

// WithOllamaRegistryURL overrides the Ollama registry URL.
func WithOllamaRegistryURL(url string) CheckerOption {
	return func(c *Checker) { c.ollamaRegistryURL = url }
}

// WithGoogleAIBaseURL overrides the Google AI API base URL.
func WithGoogleAIBaseURL(url string) CheckerOption {
	return func(c *Checker) { c.googleAIBaseURL = url }
}

// WithGoogleAPIKey sets the API key for Google AI requests.
func WithGoogleAPIKey(key string) CheckerOption {
	return func(c *Checker) { c.googleAPIKey = key }
}

// NewChecker creates a Checker with sensible defaults, modified by any supplied options.
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

// CheckOllama queries each model against the local Ollama instance and the
// remote registry, returning a ModelStatus per unique model name. Models that
// cannot be reached are silently skipped.
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
			continue
		}
		results = append(results, status)
	}

	return results, nil
}

func (c *Checker) checkOneOllamaModel(ctx context.Context, model string) (ModelStatus, error) {
	localDigest, err := c.ollamaLocalDigest(ctx, model)
	if err != nil {
		return ModelStatus{}, err
	}

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

// parseOllamaModel splits "name:tag" into its components, defaulting tag to "latest".
func parseOllamaModel(model string) (string, string) {
	parts := strings.SplitN(model, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], "latest"
}

// CheckGoogleAI queries the Google AI models endpoint and compares configured
// model names against available versions. Returns nil when no API key is set.
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
		return nil, nil
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}

	var listResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &listResp); err != nil {
		return nil, nil
	}

	available := map[string]bool{}
	for _, m := range listResp.Models {
		available[strings.TrimPrefix(m.Name, "models/")] = true
	}

	var results []ModelStatus
	for _, configured := range unique {
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

// findLatestVersion returns the lexicographically greatest model name from
// available that starts with the current name, or current itself.
func findLatestVersion(current string, available map[string]bool) string {
	best := current
	for name := range available {
		if strings.HasPrefix(name, current) && name > best {
			best = name
		}
	}
	return best
}

// RunCheck performs a full check against both Ollama and Google AI, returning a
// combined CheckResult.
func (c *Checker) RunCheck(ctx context.Context, ollamaModels, googleModels []string) CheckResult {
	result := CheckResult{CheckedAt: time.Now()}

	ollamaResults, _ := c.CheckOllama(ctx, ollamaModels)
	result.Models = append(result.Models, ollamaResults...)

	googleResults, _ := c.CheckGoogleAI(ctx, googleModels)
	result.Models = append(result.Models, googleResults...)

	return result
}
