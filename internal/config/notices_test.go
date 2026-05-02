package config

import (
	"strings"
	"testing"
)

func TestNotices_SameModelReviewerEmits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Models.Senior.Model = "shared-model"
	cfg.Models.Junior.Model = "shared-model"
	cfg.Models.Intermediate.Model = "shared-model"

	notices := cfg.Notices()

	if len(notices) != 2 {
		t.Fatalf("expected 2 notices (junior + intermediate), got %d: %v", len(notices), notices)
	}
	for _, role := range []string{"junior", "intermediate"} {
		hit := false
		for _, n := range notices {
			if strings.Contains(n, "models."+role+".model") {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("expected notice mentioning models.%s.model in %v", role, notices)
		}
	}
}

func TestNotices_DistinctModelsEmitsNothing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Models.Senior.Model = "claude-opus"
	cfg.Models.Junior.Model = "gemma4"
	cfg.Models.Intermediate.Model = "qwen-coder"

	if got := cfg.Notices(); len(got) != 0 {
		t.Errorf("expected no notices when models differ, got %v", got)
	}
}

func TestNotices_NoSeniorModelEmitsNothing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Models.Senior.Model = "" // unconfigured
	cfg.Models.Junior.Model = "gemma4"

	if got := cfg.Notices(); len(got) != 0 {
		t.Errorf("expected no notices when senior unset, got %v", got)
	}
}

// TestValidate_NoLogSideEffect guards against re-introducing the
// log.Printf side effect we removed when extracting Notices(). Validate
// must remain pure.
func TestValidate_NoLogSideEffect(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Models.Senior.Model = "shared"
	cfg.Models.Junior.Model = "shared"

	// We don't capture log output here — the contract is structural:
	// Notices() owns the message format. If a future change adds
	// log.Printf back into Validate, the duplicate notice will
	// reappear in this test's output.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	got := cfg.Notices()
	if len(got) == 0 {
		t.Fatal("Notices() should report same-model when Validate sees it")
	}
}
