package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecorder_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	rec := NewRecorder(path)

	entry := MetricEntry{
		Timestamp:  time.Now(),
		ReqID:      "req-001",
		StoryID:    "s-001",
		Phase:      "plan",
		Model:      "gemma4:26b",
		TokensIn:   1000,
		TokensOut:  500,
		DurationMs: 3200,
		Success:    true,
	}

	if err := rec.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entries, err := rec.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1, got %d", len(entries))
	}
	if entries[0].ReqID != "req-001" {
		t.Errorf("ReqID = %q", entries[0].ReqID)
	}
	if entries[0].TokensIn != 1000 {
		t.Errorf("TokensIn = %d", entries[0].TokensIn)
	}
}

func TestRecorder_MultipleEntries(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(filepath.Join(dir, "m.jsonl"))

	for i := 0; i < 5; i++ {
		if err := rec.Record(MetricEntry{
			ReqID:     "req-001",
			Phase:     "execute",
			TokensIn:  100,
			TokensOut: 50,
			Success:   true,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	entries, err := rec.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5, got %d", len(entries))
	}
}

func TestRecorder_EmptyFile(t *testing.T) {
	rec := NewRecorder(filepath.Join(t.TempDir(), "m.jsonl"))

	entries, err := rec.ReadAll()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0, got %d", len(entries))
	}
}

func TestSummarize(t *testing.T) {
	entries := []MetricEntry{
		{ReqID: "r1", StoryID: "s1", Phase: "plan", TokensIn: 100, TokensOut: 50, Success: true},
		{ReqID: "r1", StoryID: "s1", Phase: "review", TokensIn: 80, TokensOut: 40, Success: true},
		{ReqID: "r1", StoryID: "s2", Phase: "plan", TokensIn: 100, TokensOut: 50, Success: false, Escalated: true},
	}

	s := Summarize(entries)

	if s.TotalRequirements != 1 {
		t.Errorf("reqs = %d", s.TotalRequirements)
	}
	if s.TotalStories != 2 {
		t.Errorf("stories = %d", s.TotalStories)
	}
	if s.SuccessCount != 2 {
		t.Errorf("success = %d", s.SuccessCount)
	}
	if s.FailureCount != 1 {
		t.Errorf("failure = %d", s.FailureCount)
	}
	if s.EscalationCount != 1 {
		t.Errorf("escalations = %d", s.EscalationCount)
	}
	if s.ByPhase["plan"].Count != 2 {
		t.Errorf("plan count = %d", s.ByPhase["plan"].Count)
	}
}
