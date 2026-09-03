package llm

import "time"

// Read-only accessors so wiring code (and its tests) can verify how a client
// was configured without reaching into unexported fields.

// BaseURL returns the Ollama endpoint the client talks to.
func (c *OllamaClient) BaseURL() string { return c.baseURL }

// NumCtx returns the configured context window (0 = model default).
func (c *OllamaClient) NumCtx() int { return c.numCtx }

// Model returns the pinned Google model ID ("" = per-request / default).
func (c *GoogleClient) Model() string { return c.model }

// Cooldown returns how long the primary is suppressed after a quota error.
func (c *FallbackClient) Cooldown() time.Duration { return c.cooldown }

// Primary returns the client tried first.
func (c *FallbackClient) Primary() Client { return c.primary }

// Fallback returns the client used when the primary is exhausted.
func (c *FallbackClient) Fallback() Client { return c.fallback }
