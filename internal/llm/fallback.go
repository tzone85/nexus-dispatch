package llm

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// FallbackClient wraps a primary and fallback Client. When the primary
// returns a quota/rate-limit error, it transparently retries on the
// fallback and suppresses the primary for a cooldown period.
type FallbackClient struct {
	primary        Client
	fallback       Client
	quotaExhausted atomic.Bool
	cooldown       time.Duration
}

// NewFallbackClient creates a client that tries primary first, falling
// back to fallback on any error. Quota errors (429/403) trigger a
// cooldown during which primary is skipped entirely.
func NewFallbackClient(primary, fallback Client, cooldown time.Duration) *FallbackClient {
	return &FallbackClient{
		primary:  primary,
		fallback: fallback,
		cooldown: cooldown,
	}
}

func (c *FallbackClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if c.quotaExhausted.Load() {
		return c.fallback.Complete(ctx, req)
	}

	resp, err := c.primary.Complete(ctx, req)
	if err == nil {
		return resp, nil
	}

	if IsQuotaError(err) {
		log.Printf("[fallback] primary quota exhausted, switching to local Ollama for %v", c.cooldown)
		c.quotaExhausted.Store(true)
		go c.scheduleReset()
	} else {
		log.Printf("[fallback] primary error: %v, trying fallback", err)
	}

	return c.fallback.Complete(ctx, req)
}

func (c *FallbackClient) scheduleReset() {
	time.Sleep(c.cooldown)
	c.quotaExhausted.Store(false)
	log.Printf("[fallback] cooldown expired, will try primary on next request")
}
