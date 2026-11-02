package shellexec

import (
	"os"
	"slices"
	"testing"
)

func TestIsSensitiveEnvName(t *testing.T) {
	sensitive := []string{
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"GOOGLE_API_KEY",
		"GEMINI_API_KEY",
		"api_key", // case-insensitive
		"MYAPIKEY",
		"GITHUB_TOKEN",
		"SLACK_TOKEN",
		"DB_PASSWORD",
		"MYSQL_PASSWD",
		"AWS_SECRET_ACCESS_KEY",
		"SOME_CREDENTIAL",
		"SSH_PRIVATE_KEY",
	}
	for _, name := range sensitive {
		if !IsSensitiveEnvName(name) {
			t.Errorf("IsSensitiveEnvName(%q) = false, want true", name)
		}
	}

	safe := []string{
		"PATH",
		"HOME",
		"GOCACHE",
		"GOPATH",
		"GOMODCACHE",
		"GOPROXY",
		"GOFLAGS",
		"HTTPS_PROXY",
		"HTTP_PROXY",
		"NO_PROXY",
		"CGO_ENABLED",
		"TMPDIR",
		"USER",
		"SHELL",
		"LANG",
		"DATABASE_URL", // a URL, not matched by name markers (added back explicitly by callers)
	}
	for _, name := range safe {
		if IsSensitiveEnvName(name) {
			t.Errorf("IsSensitiveEnvName(%q) = true, want false", name)
		}
	}
}

func TestScrubEnv_DropsSecretsKeepsRest(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/nxd",
		"ANTHROPIC_API_KEY=sk-ant-secret",
		"OPENAI_API_KEY=sk-openai-secret",
		"GITHUB_TOKEN=ghp_secret",
		"GOCACHE=/tmp/gocache",
		"MALFORMED_NO_EQUALS", // no '=' — treated as a name, kept (not sensitive)
		"DB_PASSWORD=hunter2",
	}
	got := scrubEnv(in)

	want := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/nxd",
		"GOCACHE=/tmp/gocache",
		"MALFORMED_NO_EQUALS",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("scrubEnv mismatch\n got: %v\nwant: %v", got, want)
	}

	// The input slice must not be mutated.
	if len(in) != 8 {
		t.Fatalf("input slice was mutated: len=%d", len(in))
	}
}

func TestScrubbedEnviron_RemovesPlantedSecret(t *testing.T) {
	t.Setenv("NXD_TEST_PLANTED_API_KEY", "leak-me")
	t.Setenv("NXD_TEST_PLANTED_PLAIN", "keep-me")

	env := ScrubbedEnviron()

	for _, kv := range env {
		if kv == "NXD_TEST_PLANTED_API_KEY=leak-me" {
			t.Fatal("ScrubbedEnviron leaked a credential-named variable")
		}
	}
	// A non-secret var set on the process is preserved.
	if !slices.Contains(env, "NXD_TEST_PLANTED_PLAIN=keep-me") {
		t.Fatal("ScrubbedEnviron dropped a non-secret variable")
	}
	// Sanity: the raw environment still has the secret (so the scrub, not the
	// test harness, is what removed it).
	if !slices.Contains(os.Environ(), "NXD_TEST_PLANTED_API_KEY=leak-me") {
		t.Fatal("precondition failed: planted secret not in os.Environ()")
	}
}
