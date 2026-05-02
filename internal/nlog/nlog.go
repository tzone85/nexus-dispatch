// Package nlog wires NXD's logging onto Go's standard library slog.
//
// Two goals:
//
//  1. Every existing log.Printf call site in the repo (~190) keeps working
//     unchanged. Setup() routes the stdlib `log` package's output through
//     a slog handler, so legacy lines get level filtering and JSON output
//     for free.
//
//  2. New code can use the slog API directly via the package-level helpers
//     (Info / Warn / Error / Debug / WithCtx) for structured fields like
//     req_id, story_id, agent_id, phase.
//
// The log level and output format are driven by the workspace config
// (`workspace.log_level`, `workspace.log_format`). Set NXD_LOG_FORMAT=json
// or NXD_LOG_LEVEL=debug to override at runtime without editing the YAML.
package nlog

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

// ContextKey is the type used for slog field keys propagated through ctx.
// Define a non-string-keyed type so test code and consumers can collide-free
// inject identifiers via context.WithValue.
type ContextKey string

const (
	// CtxReqID is the requirement-ID field name and context key.
	CtxReqID ContextKey = "req_id"
	// CtxStoryID is the story-ID field name and context key.
	CtxStoryID ContextKey = "story_id"
	// CtxAgentID is the agent-ID field name and context key.
	CtxAgentID ContextKey = "agent_id"
	// CtxPhase is the pipeline phase ("plan", "execute", ...).
	CtxPhase ContextKey = "phase"
)

// componentPrefixRe matches the leading "[component]" tag many log lines
// already use (e.g. "[planner] ...", "[pipeline] ..."). When present, the
// tag becomes a structured "component" field and is stripped from the
// human message.
var componentPrefixRe = regexp.MustCompile(`^\[([a-zA-Z0-9_./:-]+)\]\s+`)

// configured guards against double-Setup; the second call no-ops to avoid
// reconfiguring slog mid-run (which can race with logger captures).
var configured atomic.Bool

// Setup configures slog as the global logger and reroutes the stdlib log
// package through it. Idempotent: subsequent calls are no-ops.
//
//	level   "debug" | "info" | "warn" | "error" — defaults to info
//	format  "text"  | "json"                    — defaults to text
//
// Environment overrides (highest priority):
//
//	NXD_LOG_LEVEL  ("debug" | "info" | "warn" | "error")
//	NXD_LOG_FORMAT ("text"  | "json")
func Setup(level, format string) {
	if !configured.CompareAndSwap(false, true) {
		return
	}
	configure(level, format)
}

// Reconfigure installs a new global slog/stdlog bridge even when Setup has
// already run. CLI commands call this after loading nxd.yaml so workspace
// log settings are honored; environment variables still override YAML.
func Reconfigure(level, format string) {
	configure(level, format)
	configured.Store(true)
}

func configure(level, format string) {
	if env := os.Getenv("NXD_LOG_LEVEL"); env != "" {
		level = env
	}
	if env := os.Getenv("NXD_LOG_FORMAT"); env != "" {
		format = env
	}

	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Bridge the stdlib log package: every log.Printf call now writes
	// through a slog adapter, gaining level filter + JSON output. We
	// strip log's own timestamp (slog adds one) by clearing flags.
	bridge := &slogBridge{logger: logger}
	log.SetFlags(0)
	log.SetOutput(bridge)
	log.SetPrefix("")
}

// SetupForTests installs a discard-handler logger for tests that want to
// silence output without losing the ability to log debug breadcrumbs.
func SetupForTests() {
	configured.Store(false)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	log.SetFlags(0)
	log.SetOutput(io.Discard)
	configured.Store(true)
}

// WithCtx returns a slog.Logger seeded with any req_id / story_id /
// agent_id / phase fields present on the context. Components that take a
// ctx should call WithCtx(ctx).Info(...) so every emission carries
// correlation IDs.
func WithCtx(ctx context.Context) *slog.Logger {
	logger := slog.Default()
	if ctx == nil {
		return logger
	}
	for _, key := range []ContextKey{CtxReqID, CtxStoryID, CtxAgentID, CtxPhase} {
		if v, ok := ctx.Value(key).(string); ok && v != "" {
			logger = logger.With(string(key), v)
		}
	}
	return logger
}

// CtxWithReq stamps a requirement ID into the context for downstream
// WithCtx calls. Returns the original ctx unchanged when reqID is empty.
func CtxWithReq(ctx context.Context, reqID string) context.Context {
	if reqID == "" {
		return ctx
	}
	return context.WithValue(ctx, CtxReqID, reqID)
}

// CtxWithStory stamps a story ID. Empty IDs are ignored.
func CtxWithStory(ctx context.Context, storyID string) context.Context {
	if storyID == "" {
		return ctx
	}
	return context.WithValue(ctx, CtxStoryID, storyID)
}

// CtxWithAgent stamps an agent ID. Empty IDs are ignored.
func CtxWithAgent(ctx context.Context, agentID string) context.Context {
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, CtxAgentID, agentID)
}

// CtxWithPhase stamps a pipeline phase ("plan", "review", ...).
func CtxWithPhase(ctx context.Context, phase string) context.Context {
	if phase == "" {
		return ctx
	}
	return context.WithValue(ctx, CtxPhase, phase)
}

// parseLevel converts a string level name to slog.Level. Unknown values
// default to Info — we never silently accept bad config that might hide
// errors.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// slogBridge implements io.Writer by forwarding each log line written
// through stdlib log into a slog record. The bridge:
//
//   - Trims the trailing newline log writes by default.
//   - Recognizes the existing "[component] message" prefix convention and
//     promotes the tag into a structured "component" field.
//   - Maps obvious severity hints in the message ("ERROR", "FATAL",
//     "WARNING") to the corresponding slog level so existing code that
//     used log.Printf for everything benefits from level filtering.
type slogBridge struct {
	logger *slog.Logger
}

// Write satisfies io.Writer. Each call corresponds to one log.Printf line.
// Returning n=len(p) is required by the log package even when we drop the
// content (e.g. via level filtering).
func (b *slogBridge) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg == "" {
		return len(p), nil
	}

	component := ""
	if m := componentPrefixRe.FindStringSubmatch(msg); m != nil {
		component = m[1]
		msg = msg[len(m[0]):]
	}

	level := classifyLevel(msg)

	logger := b.logger
	if component != "" {
		logger = logger.With("component", component)
	}
	logger.Log(context.Background(), level, msg)
	return len(p), nil
}

// classifyLevel inspects the start of a log line for severity markers.
// The keywords are case-sensitive on purpose — we match what NXD code
// already produces ("ERROR:", "FATAL:", "WARNING:").
func classifyLevel(msg string) slog.Level {
	prefix := msg
	if i := strings.IndexAny(msg, ":-—"); i > 0 && i < 32 {
		prefix = msg[:i]
	}
	upper := strings.ToUpper(prefix)
	switch {
	case strings.Contains(upper, "FATAL"), strings.Contains(upper, "PANIC"):
		return slog.LevelError
	case strings.Contains(upper, "ERROR"), strings.Contains(upper, "ERR "):
		return slog.LevelError
	case strings.Contains(upper, "WARN"):
		return slog.LevelWarn
	case strings.Contains(upper, "DEBUG"):
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// IsConfigured returns true if Setup has been called. Useful for tests.
func IsConfigured() bool {
	return configured.Load()
}

// FormatKV is a small helper for migrating call sites that want to keep
// their existing format string but add a couple of structured fields. It
// returns "msg key1=val1 key2=val2" suitable for slog.Info.
func FormatKV(msg string, kv ...any) string {
	if len(kv) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
	}
	return b.String()
}
