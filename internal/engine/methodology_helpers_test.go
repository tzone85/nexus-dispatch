package engine

import "testing"

func TestStoryOwnsCodeWithoutTest_FlagsCodeOnly(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"internal/game/board.go"},
	}
	if !storyOwnsCodeWithoutTest(s) {
		t.Errorf("code-only story should be flagged")
	}
}

func TestStoryOwnsCodeWithoutTest_HappyWithTest(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"internal/game/board.go", "internal/game/board_test.go"},
	}
	if storyOwnsCodeWithoutTest(s) {
		t.Errorf("code+test story should NOT be flagged")
	}
}

func TestStoryOwnsCodeWithoutTest_ConfigOnly(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"go.mod", "README.md", "Makefile"},
	}
	if storyOwnsCodeWithoutTest(s) {
		t.Errorf("config-only story should NOT be flagged")
	}
}

func TestStoryOwnsCodeWithoutTest_TypeScriptPaired(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"src/api/handler.ts", "src/api/handler.test.ts"},
	}
	if storyOwnsCodeWithoutTest(s) {
		t.Errorf("ts+spec story should NOT be flagged")
	}
}

func TestStoryOwnsCodeWithoutTest_PythonPaired(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"app/users.py", "tests/test_users.py"},
	}
	if storyOwnsCodeWithoutTest(s) {
		t.Errorf("python paired story should NOT be flagged")
	}
}

func TestStoryOwnsCodeWithoutTest_TestOnly(t *testing.T) {
	s := PlannedStory{
		ID:         "s-001",
		OwnedFiles: []string{"internal/game/board_test.go"},
	}
	if storyOwnsCodeWithoutTest(s) {
		t.Errorf("test-only story is fine (refactor) — should NOT be flagged")
	}
}
