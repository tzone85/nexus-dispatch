package config

import (
	"strings"
	"testing"
)

func TestCheckSchemaVersion_Equal(t *testing.T) {
	if err := CheckSchemaVersion(CurrentSchemaVersion, "/tmp/test.yaml"); err != nil {
		t.Errorf("equal version should succeed: %v", err)
	}
}

func TestCheckSchemaVersion_Empty(t *testing.T) {
	// Empty version is a soft warning — should NOT error.
	if err := CheckSchemaVersion("", "/tmp/test.yaml"); err != nil {
		t.Errorf("empty version should succeed with hint: %v", err)
	}
}

func TestCheckSchemaVersion_OlderMajor(t *testing.T) {
	// Older major must not error — backward compat mode.
	if err := CheckSchemaVersion("0.9", "/tmp/test.yaml"); err != nil {
		t.Errorf("older major should succeed (compat mode): %v", err)
	}
}

func TestCheckSchemaVersion_NewerMajor(t *testing.T) {
	// Newer major MUST error: binary doesn't understand the schema.
	err := CheckSchemaVersion("99.0", "/tmp/test.yaml")
	if err == nil {
		t.Fatal("newer major should error")
	}
	if !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Errorf("error message lacks the expected hint: %v", err)
	}
	if !strings.Contains(err.Error(), "/tmp/test.yaml") {
		t.Errorf("error message should reference the path, got: %v", err)
	}
}

func TestCheckSchemaVersion_MinorDrift(t *testing.T) {
	// Same major, different minor → log warning, succeed.
	if err := CheckSchemaVersion("1.5", "/tmp/test.yaml"); err != nil {
		t.Errorf("minor drift should succeed: %v", err)
	}
}

func TestCheckSchemaVersion_VPrefix(t *testing.T) {
	// "v1.0" should parse the same as "1.0".
	if err := CheckSchemaVersion("v"+CurrentSchemaVersion, "/tmp/test.yaml"); err != nil {
		t.Errorf("v-prefix should succeed: %v", err)
	}
}

func TestCheckSchemaVersion_Unparseable(t *testing.T) {
	// Unparseable should NOT error (graceful degradation).
	if err := CheckSchemaVersion("not-a-version", "/tmp/test.yaml"); err != nil {
		t.Errorf("unparseable should succeed silently: %v", err)
	}
}

func TestMajorOf(t *testing.T) {
	for _, tc := range []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"1.0", 1, true},
		{"v1.0", 1, true},
		{"2", 2, true},
		{"99.5.3", 99, true},
		{"v3.1.4-beta", 3, true},
		{"", 0, false},
		{"abc", 0, false},
	} {
		got, ok := majorOf(tc.in)
		if ok != tc.wantOK {
			t.Errorf("majorOf(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("majorOf(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
