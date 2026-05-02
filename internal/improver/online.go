package improver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OnlineFetcher returns curated tips fetched from a remote feed.
// Implementations MUST honour ctx cancellation and apply their own
// timeout — Improver does not impose one.
type OnlineFetcher interface {
	Fetch(ctx context.Context, info ProjectInfo) ([]Suggestion, error)
}

// HTTPFeed is the default OnlineFetcher: HTTP GET against URL, expects
// a JSON array of Suggestion objects. Disabled by default — operators
// opt in by providing a URL, and the package never makes a network call
// otherwise.
type HTTPFeed struct {
	URL     string
	Client  *http.Client
	Timeout time.Duration
}

const defaultFetchTimeout = 5 * time.Second

// Fetch satisfies OnlineFetcher.
func (h HTTPFeed) Fetch(ctx context.Context, _ ProjectInfo) ([]Suggestion, error) {
	if h.URL == "" {
		return nil, nil
	}

	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}

	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build feed request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "nxd-improver/1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}

	// Cap the body to keep a runaway feed from filling memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}

	var out []Suggestion
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode feed json: %w", err)
	}
	return out, nil
}
