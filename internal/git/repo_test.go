package git_test

import (
	"os"
	"path/filepath"
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

func TestCurrentBranch(t *testing.T) {
	repo := createTestRepo(t)
	branch, err := nxdgit.CurrentBranch(repo)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if branch == "" {
		t.Fatal("expected non-empty branch name")
	}
}

func TestBranchExists(t *testing.T) {
	repo := createTestRepo(t)
	branch, err := nxdgit.CurrentBranch(repo)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}

	if !nxdgit.BranchExists(repo, branch) {
		t.Fatalf("expected branch %s to exist", branch)
	}
	if nxdgit.BranchExists(repo, "nonexistent-branch") {
		t.Fatal("nonexistent branch should not exist")
	}
}

func TestCreateBranch(t *testing.T) {
	repo := createTestRepo(t)

	err := nxdgit.CreateBranch(repo, "feature/new")
	if err != nil {
		t.Fatalf("create branch: %v", err)
	}

	if !nxdgit.BranchExists(repo, "feature/new") {
		t.Fatal("branch should exist after creation")
	}
}

func TestDeleteBranch(t *testing.T) {
	repo := createTestRepo(t)

	if err := nxdgit.CreateBranch(repo, "to-delete"); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	if err := nxdgit.DeleteBranch(repo, "to-delete"); err != nil {
		t.Fatalf("delete branch: %v", err)
	}
	if nxdgit.BranchExists(repo, "to-delete") {
		t.Fatal("branch should not exist after deletion")
	}
}

func TestScanRepo_Go(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	stack := nxdgit.ScanRepo(dir)
	if stack.Language != "go" {
		t.Fatalf("expected 'go', got %s", stack.Language)
	}
	if stack.BuildTool != "go" {
		t.Fatalf("expected build tool 'go', got %s", stack.BuildTool)
	}
}

func TestScanRepo_Node(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	stack := nxdgit.ScanRepo(dir)
	if stack.Language != "javascript" {
		t.Fatalf("expected 'javascript', got %s", stack.Language)
	}
}

func TestScanRepo_TypeScript(t *testing.T) {
	dir := t.TempDir()
	// Both package.json and tsconfig.json present -- TypeScript should win.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	stack := nxdgit.ScanRepo(dir)
	if stack.Language != "typescript" {
		t.Fatalf("expected 'typescript', got %s", stack.Language)
	}
}

func TestScanRepo_Empty(t *testing.T) {
	dir := t.TempDir()
	stack := nxdgit.ScanRepo(dir)
	if stack.Language != "" {
		t.Fatalf("expected empty language for empty dir, got %s", stack.Language)
	}
}
