package llm

import (
	"testing"
	"time"
)

func TestInspectAccessors(t *testing.T) {
	o := NewOllamaClient("m", WithOllamaBaseURL("http://gpu:11434/"), WithOllamaNumCtx(8192))
	if o.BaseURL() != "http://gpu:11434" || o.NumCtx() != 8192 {
		t.Fatalf("ollama accessors = %q/%d", o.BaseURL(), o.NumCtx())
	}
	if d := NewOllamaClient("m"); d.BaseURL() != ollamaDefaultBaseURL || d.NumCtx() != 0 {
		t.Fatalf("defaults = %q/%d", d.BaseURL(), d.NumCtx())
	}
	g := NewGoogleClient("k", WithGoogleModel("gemini-2.5-pro"))
	if g.Model() != "gemini-2.5-pro" || NewGoogleClient("k").Model() != "" {
		t.Fatalf("google model accessor = %q", g.Model())
	}
	f := NewFallbackClient(g, o, 90*time.Second)
	if f.Cooldown() != 90*time.Second || f.Primary() != Client(g) || f.Fallback() != Client(o) {
		t.Fatal("fallback accessors must expose the constructor arguments")
	}
}
