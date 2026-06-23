package engine

import "testing"

// TestStoryIDPrefix_UniquePerRequirement pins the collision boundary that
// caused `nxd estimate` to crash with "UNIQUE constraint failed: stories.id":
// every estimate reqID ("est-YYYYMMDD-...") was truncated to its first 8 chars
// ("est-2026"), so all estimates in a calendar year shared a story-ID prefix.
func TestStoryIDPrefix_UniquePerRequirement(t *testing.T) {
	if got := storyIDPrefix("r-001"); got != "r-001" {
		t.Fatalf("short reqID should be verbatim, got %q", got)
	}
	if got := storyIDPrefix("12345678"); got != "12345678" {
		t.Fatalf("8-char reqID should be verbatim, got %q", got)
	}

	a := storyIDPrefix("est-20260623-150405")
	b := storyIDPrefix("est-20260101-000000")
	if a == b {
		t.Fatalf("distinct reqIDs collided on prefix: %q", a)
	}

	u1 := storyIDPrefix("01JZ8ABCDEFGHJKMNPQRSTVWX1")
	u2 := storyIDPrefix("01JZ8ABCDEFGHJKMNPQRSTVWX2")
	if u1 == u2 {
		t.Fatalf("near-simultaneous ULID reqIDs collided on prefix: %q", u1)
	}

	if storyIDPrefix("est-20260623-150405") != a {
		t.Fatal("prefix not deterministic for identical reqID")
	}
	if len(a) > 8 {
		t.Fatalf("prefix should be ≤8 chars, got %d (%q)", len(a), a)
	}
}
