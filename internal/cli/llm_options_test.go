package cli

import (
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// The role's ModelConfig and models.ollama_host must reach the constructed
// clients: before this wiring google_model, fallback_cooldown_s, num_ctx and
// ollama_host were parsed and documented but never applied.

func TestLLMOptsFor(t *testing.T) {
	mc := config.ModelConfig{Provider: "google+ollama", Model: "qwen", GoogleModel: "gemini-2.5-pro", NumCtx: 4096, FallbackCooldownS: 30}
	o := llmOptsFor(mc, config.ModelsConfig{OllamaHost: "10.0.0.5:11434"})
	if o.Provider != "google+ollama" || o.Model != mc || o.OllamaHost != "10.0.0.5:11434" {
		t.Fatalf("llmOptsFor = %+v", o)
	}
}

func TestFallbackCooldown(t *testing.T) {
	cases := map[int]time.Duration{0: 60 * time.Second, -5: 60 * time.Second, 1: time.Second, 120: 120 * time.Second}
	for secs, want := range cases {
		if got := fallbackCooldown(config.ModelConfig{FallbackCooldownS: secs}); got != want {
			t.Errorf("fallbackCooldown(%d) = %v, want %v", secs, got, want)
		}
	}
}

func TestBuildLLMClientDefault_Ollama(t *testing.T) {
	build := func(t *testing.T, o llmBuildOpts) *llm.OllamaClient {
		t.Helper()
		c, err := buildLLMClientDefault(o)
		if err != nil {
			t.Fatal(err)
		}
		oc, ok := c.(*llm.OllamaClient)
		if !ok {
			t.Fatalf("got %T, want *llm.OllamaClient", c)
		}
		return oc
	}

	t.Run("defaults", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "")
		oc := build(t, llmBuildOpts{Provider: "ollama"})
		if oc.BaseURL() != "http://localhost:11434" || oc.NumCtx() != 0 {
			t.Fatalf("base=%q numctx=%d", oc.BaseURL(), oc.NumCtx())
		}
	})

	t.Run("config host wins over env and gets a scheme; num_ctx applied", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "http://env-box:11434")
		oc := build(t, llmBuildOpts{Provider: "ollama", OllamaHost: "10.0.0.5:11434", Model: config.ModelConfig{NumCtx: 16384}})
		if oc.BaseURL() != "http://10.0.0.5:11434" {
			t.Fatalf("base = %q, want the configured host", oc.BaseURL())
		}
		if oc.NumCtx() != 16384 {
			t.Fatalf("numctx = %d, want 16384", oc.NumCtx())
		}
	})

	t.Run("env used when config empty", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "env-box:11434/")
		oc := build(t, llmBuildOpts{Provider: "ollama"})
		if oc.BaseURL() != "http://env-box:11434" {
			t.Fatalf("base = %q", oc.BaseURL())
		}
	})
}

func TestBuildLLMClientDefault_Google(t *testing.T) {
	t.Setenv("GOOGLE_AI_API_KEY", "k")
	c, err := buildLLMClientDefault(llmBuildOpts{Provider: "google", Model: config.ModelConfig{GoogleModel: "gemini-2.5-flash"}})
	if err != nil {
		t.Fatal(err)
	}
	gc, ok := c.(*llm.GoogleClient)
	if !ok || gc.Model() != "gemini-2.5-flash" {
		t.Fatalf("got %T model=%q", c, gc.Model())
	}
	c, _ = buildLLMClientDefault(llmBuildOpts{Provider: "google"})
	if c.(*llm.GoogleClient).Model() != "" {
		t.Fatal("empty google_model must leave the client's default")
	}
}

func TestBuildLLMClientDefault_GoogleOllamaFallback(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Run("with key: fallback client carries cooldown, model and ollama options", func(t *testing.T) {
		t.Setenv("GOOGLE_AI_API_KEY", "k")
		o := llmBuildOpts{Provider: "google+ollama", OllamaHost: "gpu:11434",
			Model: config.ModelConfig{GoogleModel: "gemini-2.5-pro", FallbackCooldownS: 15, NumCtx: 2048}}
		c, err := buildLLMClientDefault(o)
		if err != nil {
			t.Fatal(err)
		}
		fc, ok := c.(*llm.FallbackClient)
		if !ok {
			t.Fatalf("got %T, want *llm.FallbackClient", c)
		}
		if fc.Cooldown() != 15*time.Second {
			t.Fatalf("cooldown = %v, want 15s", fc.Cooldown())
		}
		if g := fc.Primary().(*llm.GoogleClient); g.Model() != "gemini-2.5-pro" {
			t.Fatalf("primary model = %q", g.Model())
		}
		oc := fc.Fallback().(*llm.OllamaClient)
		if oc.BaseURL() != "http://gpu:11434" || oc.NumCtx() != 2048 {
			t.Fatalf("fallback ollama base=%q numctx=%d", oc.BaseURL(), oc.NumCtx())
		}
	})

	t.Run("cooldown defaults to 60s", func(t *testing.T) {
		t.Setenv("GOOGLE_AI_API_KEY", "k")
		c, _ := buildLLMClientDefault(llmBuildOpts{Provider: "google+ollama"})
		if got := c.(*llm.FallbackClient).Cooldown(); got != 60*time.Second {
			t.Fatalf("cooldown = %v, want 60s", got)
		}
	})

	t.Run("without key: plain ollama with options", func(t *testing.T) {
		t.Setenv("GOOGLE_AI_API_KEY", "")
		c, err := buildLLMClientDefault(llmBuildOpts{Provider: "google+ollama", Model: config.ModelConfig{NumCtx: 512}})
		if err != nil {
			t.Fatal(err)
		}
		oc, ok := c.(*llm.OllamaClient)
		if !ok || oc.NumCtx() != 512 {
			t.Fatalf("got %T numctx=%d", c, oc.NumCtx())
		}
	})
}

// TestBuildLLMClientFor_UsesSeamAndSanitizes: the role-aware builder must go
// through the same test seam as buildLLMClient (so mocks apply to every
// command) and wrap the result in the sanitizing client.
func TestBuildLLMClientFor_UsesSeamAndSanitizes(t *testing.T) {
	original := buildLLMClientFunc
	t.Cleanup(func() { buildLLMClientFunc = original })
	var seen llmBuildOpts
	buildLLMClientFunc = func(o llmBuildOpts, godmode ...bool) (llm.Client, error) {
		seen = o
		return llm.NewReplayClient(), nil
	}
	mc := config.ModelConfig{Provider: "ollama", NumCtx: 99}
	c, err := buildLLMClientFor(llmOptsFor(mc, config.ModelsConfig{OllamaHost: "h:1"}))
	if err != nil {
		t.Fatal(err)
	}
	if seen.Model.NumCtx != 99 || seen.OllamaHost != "h:1" || seen.Provider != "ollama" {
		t.Fatalf("seam received %+v", seen)
	}
	if _, ok := c.(*llm.SanitizingClient); !ok {
		t.Fatalf("got %T, want *llm.SanitizingClient", c)
	}
	// The provider-only helper is the same path with empty options.
	if _, err := buildLLMClient("ollama"); err != nil || seen.Provider != "ollama" || seen.OllamaHost != "" {
		t.Fatalf("buildLLMClient: err=%v seen=%+v", err, seen)
	}
}
