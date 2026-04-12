package git

import (
	"errors"
	"testing"
)

func TestConflictError_Error(t *testing.T) {
	err := &ConflictError{Output: "CONFLICT in main.go"}
	s := err.Error()
	if s != "merge conflict: CONFLICT in main.go" {
		t.Errorf("Error() = %q", s)
	}
}

func TestIsConflict_True(t *testing.T) {
	err := &ConflictError{Output: "test"}
	if !IsConflict(err) {
		t.Error("expected IsConflict to return true for *ConflictError")
	}
}

func TestIsConflict_False(t *testing.T) {
	if IsConflict(errors.New("not a conflict")) {
		t.Error("expected IsConflict to return false for non-ConflictError")
	}
}

func TestIsConflict_Nil(t *testing.T) {
	if IsConflict(nil) {
		t.Error("expected IsConflict to return false for nil")
	}
}

func TestIsConflictInternal_CONFLICT(t *testing.T) {
	if !isConflict("error: CONFLICT (content): merge conflict in foo.go") {
		t.Error("expected true for CONFLICT keyword")
	}
}

func TestIsConflictInternal_CouldNotApply(t *testing.T) {
	if !isConflict("error: could not apply abc123") {
		t.Error("expected true for 'could not apply'")
	}
}

func TestIsConflictInternal_ResolveAll(t *testing.T) {
	if !isConflict("Resolve all conflicts manually") {
		t.Error("expected true for 'Resolve all conflicts'")
	}
}

func TestIsConflictInternal_NoConflict(t *testing.T) {
	if isConflict("Already up to date.") {
		t.Error("expected false for clean rebase output")
	}
}
