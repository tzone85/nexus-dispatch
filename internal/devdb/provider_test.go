package devdb_test

import (
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

func TestStoryOutcome_String(t *testing.T) {
	cases := []struct {
		out  devdb.StoryOutcome
		want string
	}{
		{devdb.OutcomeSuccess, "success"},
		{devdb.OutcomeFailed, "failed"},
		{devdb.OutcomePaused, "paused"},
	}
	for _, c := range cases {
		if got := c.out.String(); got != c.want {
			t.Errorf("StoryOutcome(%d).String() = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestErrors_AreDistinct(t *testing.T) {
	errs := []error{
		devdb.ErrNotFound,
		devdb.ErrAlreadyExists,
		devdb.ErrProviderDown,
		devdb.ErrInvalidName,
		devdb.ErrTemplateMiss,
		devdb.ErrUnsupported,
	}
	seen := map[error]bool{}
	for _, e := range errs {
		if seen[e] {
			t.Errorf("duplicate sentinel error: %v", e)
		}
		seen[e] = true
	}
}

func TestErrors_IsPreserved(t *testing.T) {
	wrapped := errors.New("inner: " + devdb.ErrNotFound.Error())
	combined := errors.Join(wrapped, devdb.ErrNotFound)
	if !errors.Is(combined, devdb.ErrNotFound) {
		t.Errorf("errors.Is should find ErrNotFound through Join")
	}
}

func TestDB_ZeroValue(t *testing.T) {
	var d devdb.DB
	if d.ID != "" || d.Name != "" || d.Provider != "" || !d.CreatedAt.IsZero() {
		t.Errorf("zero-value DB should be empty, got %+v", d)
	}
}

func TestCreateOpts_DefaultsAreZero(t *testing.T) {
	var o devdb.CreateOpts
	if o.ReadOnly || o.WaitReady || o.WaitTimeout != 0 {
		t.Errorf("zero-value CreateOpts should have zero defaults, got %+v", o)
	}
}
