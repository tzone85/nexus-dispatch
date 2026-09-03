package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestOllamaHost(t *testing.T) {
	tests := []struct {
		name, env, cfg, want string
	}{
		{"default", "", "", "http://localhost:11434"},
		{"config host:port gets scheme", "", "10.0.0.5:11434", "http://10.0.0.5:11434"},
		{"config with scheme kept", "", "https://ollama.example.com/", "https://ollama.example.com"},
		{"config wins over env", "gpu-box:11434", "10.0.0.5:11434", "http://10.0.0.5:11434"},
		{"env used when config empty", "gpu-box:11434", "", "http://gpu-box:11434"},
		{"env with scheme", "http://gpu-box:11434/", "", "http://gpu-box:11434"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", tc.env)
			if got := ollamaHost(tc.cfg); got != tc.want {
				t.Errorf("ollamaHost(%q) with env %q = %q, want %q", tc.cfg, tc.env, got, tc.want)
			}
		})
	}
}

// Defect 9: doctor hard-coded localhost:11434 and failed on remote Ollama.
func TestCheckOllamaAt_RemoteServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	res := checkOllamaAt(srv.URL)
	if res.Status != "ok" || !strings.Contains(res.Message, strings.TrimPrefix(srv.URL, "http://")) {
		t.Errorf("remote ok: %+v", res)
	}

	srv.Close()
	res = checkOllamaAt(srv.URL)
	if res.Status != "fail" || !strings.Contains(res.Message, srv.URL) || !strings.Contains(res.Message, "OLLAMA_HOST") {
		t.Errorf("remote fail should name the URL and the remote hint: %+v", res)
	}

	// The localhost default keeps the "ollama serve" hint.
	res = checkOllamaAt("http://127.0.0.1:1")
	if res.Status != "fail" || !strings.Contains(res.Message, "Ollama not reachable at http://127.0.0.1:1") {
		t.Errorf("localhost fail: %+v", res)
	}
	res = checkOllamaAt(defaultOllamaHost + "x") // guaranteed-unreachable variant of the default
	if res.Status != "fail" {
		t.Errorf("expected fail, got %+v", res)
	}
}

// checkGo must not hard-fail on non-Go repositories.
func TestCheckGoFor(t *testing.T) {
	// Force the "go not found" branch by emptying PATH.
	t.Setenv("PATH", t.TempDir())

	nonGo := t.TempDir()
	if res := checkGoFor(nonGo); res.Status != "warn" || !strings.Contains(res.Message, "only needed for Go") {
		t.Errorf("non-Go repo should warn: %+v", res)
	}

	goRepo := t.TempDir()
	os.WriteFile(filepath.Join(goRepo, "go.mod"), []byte("module x\n"), 0o644)
	if res := checkGoFor(goRepo); res.Status != "fail" || !strings.Contains(res.Message, "go.mod is present") {
		t.Errorf("Go repo should fail: %+v", res)
	}

	if res := checkGoFor(""); res.Status != "fail" {
		t.Errorf("unknown dir keeps strict behaviour: %+v", res)
	}
	if !isGoRepo("") || !isGoRepo(goRepo) || isGoRepo(nonGo) {
		t.Error("isGoRepo")
	}
}

func TestDoctor_JSONOutput(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execCmd(t, newDoctorCmd(), env.Config, "--json")
	// Ollama is not running in CI, so a fail exit is expected; the payload
	// must still be valid JSON with the summary counts.
	var res struct {
		Checks   []checkResult `json:"checks"`
		Passed   int           `json:"passed"`
		Warnings int           `json:"warnings"`
		Failed   int           `json:"failed"`
	}
	// Decode only the first JSON value: cobra appends its own "Error:" line
	// to the buffer when the command is executed outside the root.
	if jerr := json.NewDecoder(strings.NewReader(out)).Decode(&res); jerr != nil {
		t.Fatalf("invalid JSON (%v): %v\n%s", err, jerr, out)
	}
	if len(res.Checks) == 0 || res.Passed+res.Warnings+res.Failed != len(res.Checks) {
		t.Errorf("summary counts inconsistent: %+v", res)
	}
	if res.Failed > 0 && err == nil {
		t.Error("failed checks must still produce a non-zero exit with --json")
	}
	if strings.Contains(out, "NXD Doctor") {
		t.Error("--json output must not include the human header")
	}
}

func TestAgents_JSON(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execCmd(t, newAgentsCmd(), env.Config, "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty agents --json = %q, %v; want []", out, err)
	}
	seedTestAgent(t, env, "agent-1", "junior", "nxd-s1")
	out, err = execCmd(t, newAgentsCmd(), env.Config, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var agents []state.Agent
	if err := json.Unmarshal([]byte(out), &agents); err != nil || len(agents) != 1 || agents[0].ID != "agent-1" {
		t.Errorf("agents --json = %s (%v)", out, err)
	}
}

func TestEscalations_JSON(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execCmd(t, newEscalationsCmd(), env.Config, "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty escalations --json = %q, %v; want []", out, err)
	}
	seedTestReq(t, env, "r1", "t", "/r")
	seedTestStory(t, env, "s1", "r1", "s", 1)
	seedTestEscalation(t, env, "s1", "junior", "stuck")
	out, err = execCmd(t, newEscalationsCmd(), env.Config, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var esc []state.Escalation
	if err := json.Unmarshal([]byte(out), &esc); err != nil || len(esc) != 1 || esc[0].Reason != "stuck" {
		t.Errorf("escalations --json = %s (%v)", out, err)
	}
}

func TestEvents_JSON(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execCmd(t, newEventsCmd(), env.Config, "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty events --json = %q, %v; want []", out, err)
	}
	seedTestReq(t, env, "r1", "Title", "/r")
	seedTestStory(t, env, "s1", "r1", "s", 1)
	out, err = execCmd(t, newEventsCmd(), env.Config, "--json", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	var events []eventJSON
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(events) != 1 || events[0].Type != state.EventStoryCreated {
		t.Errorf("newest-first with limit 1 should be STORY_CREATED, got %+v", events)
	}
	if events[0].Payload["id"] != "s1" {
		t.Errorf("payload must be decoded, got %v", events[0].Payload)
	}
}
