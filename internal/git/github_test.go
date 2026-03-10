package git_test

import (
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

func TestGHAvailable(t *testing.T) {
	// Just verify it doesn't panic.
	_ = nxdgit.GHAvailable()
}

// PR tests (CreatePR, MergePR, GetPRStatus) require a real GitHub repo with
// authentication, so they are exercised in E2E tests rather than unit tests.
