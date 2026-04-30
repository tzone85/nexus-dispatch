package sanitize

import "testing"

func TestSafeJoin_Within(t *testing.T) {
	got, err := SafeJoin("/var/data", "story-1/qa.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/var/data/story-1/qa.json" {
		t.Errorf("got %q", got)
	}
}

func TestSafeJoin_RejectsTraversal(t *testing.T) {
	for _, rel := range []string{
		"../etc/passwd",
		"story-1/../../etc/passwd",
		"../../../root",
		"/etc/passwd",
	} {
		if _, err := SafeJoin("/var/data", rel); err == nil {
			t.Errorf("expected error for %q", rel)
		}
	}
}

func TestSafeJoin_RootEqualsResult(t *testing.T) {
	got, err := SafeJoin("/var/data", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/var/data" {
		t.Errorf("got %q", got)
	}
}

func TestSafeJoin_EmptyRoot(t *testing.T) {
	if _, err := SafeJoin("", "anything"); err == nil {
		t.Error("expected error for empty root")
	}
}

func TestValidIdentifier(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"story-1", true},
		{"a_b.c", true},
		{"abc123", true},
		{"", false},
		{"a/b", false},
		{"a b", false},
		{"a;b", false},
		{"../etc", false},
	} {
		if got := ValidIdentifier(tc.in); got != tc.want {
			t.Errorf("ValidIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
