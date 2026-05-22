package devdb_test

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

func TestFormatDBName_Basic(t *testing.T) {
	got := devdb.FormatDBName("nxd", "mukuru-api", "a8cbef1f-3a")
	want := "nxd-mukuru-api-a8cbef1f-3a"
	if got != want {
		t.Errorf("FormatDBName = %q, want %q", got, want)
	}
}

func TestFormatDBName_LowercasesProject(t *testing.T) {
	got := devdb.FormatDBName("nxd", "MyProject", "abc-1")
	if got != "nxd-myproject-abc-1" {
		t.Errorf("got %q, want lowercase project", got)
	}
}

func TestFormatDBName_StripsInvalidProjectChars(t *testing.T) {
	got := devdb.FormatDBName("nxd", "foo_bar.baz/qux", "story-1")
	if got != "nxd-foo-bar-baz-qux-story-1" {
		t.Errorf("got %q, want underscores/dots/slashes replaced", got)
	}
}

func TestFormatDBName_TruncatesProject(t *testing.T) {
	long := strings.Repeat("a", 50)
	got := devdb.FormatDBName("nxd", long, "story-1")
	if len(got) > 63 {
		t.Errorf("name length %d exceeds Postgres 63-char limit: %q", len(got), got)
	}
}

func TestIsValid(t *testing.T) {
	cases := map[string]bool{
		"nxd-mukuru-api-a8cbef1f-3a": true,
		"a":                          true,
		"a-b-c":                      true,
		"":                           false,
		"-abc":                       false,
		"1abc":                       false,
		"ABC":                        false,
		"foo_bar":                    false,
		strings.Repeat("a", 64):      false,
		strings.Repeat("a", 63):      true,
	}
	for name, want := range cases {
		if got := devdb.IsValid(name); got != want {
			t.Errorf("IsValid(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParseStoryID_Roundtrip(t *testing.T) {
	for _, story := range []string{"a8cbef1f-3a", "b9fde001-1c", "zz-00"} {
		name := devdb.FormatDBName("nxd", "myproj", story)
		got := devdb.ParseStoryID("nxd", name)
		if got != story {
			t.Errorf("ParseStoryID(%q) = %q, want %q", name, got, story)
		}
	}
}

func TestParseStoryID_WrongPrefix(t *testing.T) {
	got := devdb.ParseStoryID("vxd", "nxd-myproj-story-1")
	if got != "" {
		t.Errorf("ParseStoryID with wrong prefix = %q, want empty", got)
	}
}
