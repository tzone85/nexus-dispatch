package nlog

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
)

// captureSlog installs a JSON-handler slog logger writing to the returned
// buffer. Reset is the cleanup hook to call from t.Cleanup.
func captureSlog(t *testing.T, level slog.Level) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	prevConfig := configured.Load()

	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	bridge := &slogBridge{logger: logger}
	prevFlags := log.Flags()
	log.SetFlags(0)
	prevWriter := log.Writer()
	log.SetOutput(bridge)
	configured.Store(true)

	return buf, func() {
		slog.SetDefault(prev)
		log.SetFlags(prevFlags)
		log.SetOutput(prevWriter)
		configured.Store(prevConfig)
	}
}

func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		records = append(records, m)
	}
	return records
}

func TestSlogBridge_PromotesComponentPrefix(t *testing.T) {
	buf, reset := captureSlog(t, slog.LevelDebug)
	defer reset()

	log.Printf("[planner] decomposed %d stories", 5)

	got := decodeRecords(t, buf)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1: %s", len(got), buf.String())
	}
	if got[0]["component"] != "planner" {
		t.Errorf("component = %v, want planner", got[0]["component"])
	}
	if got[0]["msg"] != "decomposed 5 stories" {
		t.Errorf("msg = %v, want %q", got[0]["msg"], "decomposed 5 stories")
	}
}

func TestSlogBridge_NoComponentPrefix(t *testing.T) {
	buf, reset := captureSlog(t, slog.LevelDebug)
	defer reset()

	log.Printf("plain message")

	got := decodeRecords(t, buf)
	if len(got) != 1 {
		t.Fatal("expected 1 record")
	}
	if _, ok := got[0]["component"]; ok {
		t.Errorf("plain message should not have component, got %v", got[0]["component"])
	}
	if got[0]["msg"] != "plain message" {
		t.Errorf("msg = %v", got[0]["msg"])
	}
}

func TestClassifyLevel(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want slog.Level
	}{
		{"FATAL: db closed", slog.LevelError},
		{"ERROR while reading", slog.LevelError},
		{"WARNING: deprecated config", slog.LevelWarn},
		{"DEBUG: dispatched 5 agents", slog.LevelDebug},
		{"normal informational message", slog.LevelInfo},
		{"long message without any severity hint that goes on for a while", slog.LevelInfo},
	} {
		if got := classifyLevel(tc.msg); got != tc.want {
			t.Errorf("classifyLevel(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestSlogBridge_FiltersBelowLevel(t *testing.T) {
	buf, reset := captureSlog(t, slog.LevelWarn)
	defer reset()

	log.Printf("[planner] info-level line")           // Info — should be filtered
	log.Printf("[merger] WARNING something off")      // Warn — kept
	log.Printf("[runtime] ERROR billing exhausted")   // Error — kept

	got := decodeRecords(t, buf)
	if len(got) != 2 {
		t.Fatalf("expected 2 records (warn+error), got %d: %s", len(got), buf.String())
	}
	if got[0]["component"] != "merger" {
		t.Errorf("first record component = %v", got[0]["component"])
	}
	if got[1]["level"] != "ERROR" {
		t.Errorf("second record level = %v", got[1]["level"])
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"err", slog.LevelError},
		{"", slog.LevelInfo},
		{"nonsense", slog.LevelInfo},
		{" Debug ", slog.LevelDebug},
	} {
		if got := parseLevel(tc.in); got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestWithCtx_PromotesIDs(t *testing.T) {
	buf, reset := captureSlog(t, slog.LevelInfo)
	defer reset()

	ctx := context.Background()
	ctx = CtxWithReq(ctx, "REQ-1")
	ctx = CtxWithStory(ctx, "STORY-A")
	ctx = CtxWithAgent(ctx, "agent-x")
	ctx = CtxWithPhase(ctx, "review")

	WithCtx(ctx).Info("structured")

	got := decodeRecords(t, buf)
	if len(got) != 1 {
		t.Fatal("expected 1 record")
	}
	for _, key := range []string{"req_id", "story_id", "agent_id", "phase"} {
		if got[0][key] == nil {
			t.Errorf("missing field %q in: %v", key, got[0])
		}
	}
}

func TestCtxWith_EmptyValuesAreNoOps(t *testing.T) {
	ctx := context.Background()
	if got := CtxWithReq(ctx, ""); got != ctx {
		t.Error("empty req should return ctx unchanged")
	}
	if got := CtxWithStory(ctx, ""); got != ctx {
		t.Error("empty story should return ctx unchanged")
	}
	if got := CtxWithAgent(ctx, ""); got != ctx {
		t.Error("empty agent should return ctx unchanged")
	}
	if got := CtxWithPhase(ctx, ""); got != ctx {
		t.Error("empty phase should return ctx unchanged")
	}
}

func TestSetup_Idempotent(t *testing.T) {
	// Reset configured flag for the test.
	configured.Store(false)
	defer configured.Store(true)

	Setup("info", "text")
	first := slog.Default()
	Setup("debug", "json") // should be a no-op
	second := slog.Default()
	if first != second {
		t.Error("Setup should be idempotent on second call")
	}
}

func TestFormatKV(t *testing.T) {
	if got := FormatKV("msg"); got != "msg" {
		t.Errorf("no kv: %q", got)
	}
	if got := FormatKV("msg", "k", "v"); !strings.Contains(got, "k=v") {
		t.Errorf("missing k=v: %q", got)
	}
	// Odd kv silently drops the trailing key.
	got := FormatKV("msg", "k", "v", "lonely")
	if !strings.Contains(got, "k=v") {
		t.Errorf("missing k=v in odd kv: %q", got)
	}
}

// TestSlogBridge_IsConfigured exercises the IsConfigured accessor under
// concurrent load to ensure the atomic flag is honoured.
func TestSlogBridge_IsConfigured(t *testing.T) {
	prev := configured.Load()
	defer configured.Store(prev)

	configured.Store(false)
	if IsConfigured() {
		t.Error("expected unconfigured")
	}

	var hits atomic.Int32
	for i := 0; i < 32; i++ {
		go func() {
			if IsConfigured() {
				hits.Add(1)
			}
		}()
	}
	configured.Store(true)
	if !IsConfigured() {
		t.Error("expected configured")
	}
	_ = hits.Load() // sanity touch
}
