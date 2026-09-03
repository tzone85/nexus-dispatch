package runtime

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestValidateDockerExtraFlags_Allowlist(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		ok    bool
	}{
		{"empty", nil, true},
		{"resource limits =", []string{"--cpus=2", "--memory=512m"}, true},
		{"resource limits spaced", []string{"--cpus", "2", "--memory", "512m"}, true},
		{"read-only + tmpfs", []string{"--read-only", "--tmpfs", "/tmp"}, true},
		{"user and label", []string{"-u", "1000:1000", "--label", "nxd=1"}, true},
		{"privileged", []string{"--privileged"}, false},
		{"cap-add", []string{"--cap-add", "SYS_ADMIN"}, false},
		{"cap-add =", []string{"--cap-add=SYS_ADMIN"}, false},
		{"device", []string{"--device", "/dev/sda"}, false},
		{"pid host", []string{"--pid=host"}, false},
		{"pid container", []string{"--pid=container:x"}, false},
		{"ipc host", []string{"--ipc=host"}, false},
		{"userns host", []string{"--userns=host"}, false},
		{"security-opt", []string{"--security-opt=label=disable"}, false},
		{"security-opt seccomp", []string{"--security-opt", "seccomp=unconfined"}, false},
		{"volume", []string{"-v", "/:/host"}, false},
		{"mount", []string{"--mount", "type=bind,src=/,dst=/host"}, false},
		{"network override", []string{"--network=host"}, false},
		{"unknown flag", []string{"--foo"}, false},
		{"bare value", []string{"value"}, false},
		{"metachar", []string{"--cpus=2;rm"}, false},
		{"dangling value", []string{"--cpus"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDockerExtraFlags(tc.flags)
			if (err == nil) != tc.ok {
				t.Errorf("validateDockerExtraFlags(%q) = %v, want ok=%v", tc.flags, err, tc.ok)
			}
		})
	}
}

func TestDockerRunner_Run_RejectsHostNetworkAndBadFlags(t *testing.T) {
	called := false
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	pe := PreparedExecution{Command: "x", WorkDir: t.TempDir(), SessionName: "s"}
	if err := NewDockerRunner(DockerConfig{Image: "i", Network: "host"}).Run(pe); err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("host network must be rejected: %v", err)
	}
	if err := NewDockerRunner(DockerConfig{Image: "i", ExtraFlags: []string{"--privileged"}}).Run(pe); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("privileged must be rejected: %v", err)
	}
	if called {
		t.Error("docker must not be launched when validation fails")
	}
}

func TestDockerRunner_Run_EnvFileContentAndNoEnvWithoutVars(t *testing.T) {
	var captured []string
	var envFileContent string
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		captured = args
		for i, a := range args {
			if a == "--env-file" {
				data, _ := readFileString(args[i+1])
				envFileContent = data
			}
		}
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	dir := t.TempDir()
	r := NewDockerRunner(DockerConfig{Image: "img"})
	if err := r.Run(PreparedExecution{Command: "c", WorkDir: dir, SessionName: "s", Env: map[string]string{"K": "v=1"}}); err != nil {
		t.Fatal(err)
	}
	if envFileContent != "K=v=1\n" {
		t.Errorf("env file content = %q", envFileContent)
	}
	if err := r.Run(PreparedExecution{Command: "c", WorkDir: dir, SessionName: "s"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(captured, " "), "--env-file") {
		t.Errorf("no --env-file without env vars: %v", captured)
	}
	if got := strings.Join(captured, " "); !strings.HasPrefix(got, "run -d --name s --network none -w /workspace -v "+dir+":/workspace --cap-drop ALL --security-opt no-new-privileges img sh -c c") {
		t.Errorf("argv = %s", got)
	}
	if err := r.Run(PreparedExecution{Command: "c", WorkDir: dir, SessionName: "s", Env: map[string]string{"K": "a\nb"}}); err == nil {
		t.Error("newline in env value must be rejected")
	}
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
