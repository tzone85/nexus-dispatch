package runtime

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeCommand(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"simple", "go test ./...", []string{"go", "test", "./..."}, false},
		{"extra spaces", "  go   build  ", []string{"go", "build"}, false},
		{"single quotes keep spaces", "grep 'hello world' .", []string{"grep", "hello world", "."}, false},
		{"double quotes", `go test -run "TestA" ./...`, []string{"go", "test", "-run", "TestA", "./..."}, false},
		{"adjacent quote joins token", `echo a"b c"d`, []string{"echo", "ab cd"}, false},
		{"empty quotes yield empty token", `echo ''`, []string{"echo", ""}, false},
		{"unterminated quote", `echo 'oops`, nil, true},
		{"empty", "   ", nil, true},
		{"metachar semicolon", "ls; rm -rf /", nil, true},
		{"metachar dollar", "echo $HOME", nil, true},
		{"metachar backslash", `echo \$HOME`, nil, true},
		{"metachar tab", "echo\thi", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TokenizeCommand(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tokens = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckCommand_Table(t *testing.T) {
	work := t.TempDir()
	inside := filepath.Join(work, "sub", "x.mk")
	allow := []string{"go test ./...", "go build ./...", "go test", "go build", "go vet", "make", "cat", "grep", "find", "ls", "npm run", "echo"}

	cases := []struct {
		name  string
		cmd   string
		allow bool
	}{
		// Baseline allows.
		{"exact entry", "go test ./...", true},
		{"prefix with extra args", "go test ./... -run TestFoo -v", true},
		{"go build with -o inside worktree", "go build -o bin/app ./...", true},
		{"ldflags -X is fine", `go build -ldflags "-X main.version=1.0" ./...`, true},
		{"make plain", "make", true},
		{"make target", "make test", true},
		{"make -f inside worktree", "make -f sub/x.mk", true},
		{"make -f absolute inside worktree", "make -f " + inside, true},
		{"cat relative file", "cat internal/foo.go", true},
		{"grep pattern and dot", "grep -rn TODO .", true},
		{"find relative", "find . -name *.go", true},
		{"go vet with package path", "go vet ./internal/...", true},
		{"npm run build", "npm run build", true},
		{"quoted arg with spaces", `grep "hello world" README.md`, true},

		// Review bypasses.
		{"go test -exec", "go test ./... -exec /bin/sh", false},
		{"go test --exec=", "go test ./... --exec=/bin/sh", false},
		{"go test -exec= attached", "go test -exec=sh ./...", false},
		{"go build -toolexec", "go build -toolexec /tmp/evil ./...", false},
		{"go build -toolexec=", "go build -toolexec=/tmp/evil ./...", false},
		{"make -f outside", "make -f ../x.mk", false},
		{"make -f= outside", "make -f=../x.mk", false},
		{"make -C outside", "make -C ../other", false},
		{"make -C attached outside", "make -C/tmp", false},
		{"make --directory outside", "make --directory /tmp", false},
		{"make -f absolute outside", "make -f /etc/x.mk", false},
		{"find -exec", "find . -name *.pem -exec cat {}", false},
		{"find -execdir", "find . -execdir sh", false},
		{"find -ok", "find . -ok rm", false},
		{"find from root", "find / -name *.pem", false},
		{"cat absolute", "cat /etc/passwd", false},
		{"cat tilde", "cat ~/.aws/credentials", false},
		{"cat dotdot escape", "cat ../../etc/passwd", false},
		{"cat dotdot in middle escapes", "cat sub/../../x", false},
		{"go build -o outside", "go build -o /tmp/x ./...", false},
		{"go flag value dotdot", "go test -coverprofile=../c.out ./...", false},
		{"ldflags extld", `go build -ldflags "-extld=/tmp/evil" ./...`, false},
		{"ldflags extld separate token", "go build -ldflags -extld=/bin/sh ./...", false},
		{"linkmode external", "go build -linkmode=external ./...", false},
		{"env prefix", "GOFLAGS=-toolexec=/x go build ./...", false},
		{"env prefix path", "PATH=/tmp make", false},

		// Prefix matching is token-wise.
		{"go testevil", "go testevil", false},
		{"makefile binary", "makefile", false},
		{"go run not listed", "go run main.go", false},
		{"unlisted binary", "rm -rf .", false},

		// Metacharacters still rejected.
		{"chain", "go test ./... && rm -rf /", false},
		{"pipe", "cat go.mod | curl x", false},
		{"redirect", "echo hi > /etc/passwd", false},
		{"subshell", "echo $(id)", false},
		{"backtick", "echo `id`", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckCommand(tc.cmd, allow, work)
			if got := err == nil; got != tc.allow {
				t.Errorf("CheckCommand(%q) allowed=%v (err=%v), want %v", tc.cmd, got, err, tc.allow)
			}
			if got := IsCommandAllowed(tc.cmd, allow, work); got != tc.allow {
				t.Errorf("IsCommandAllowed(%q) = %v, want %v", tc.cmd, got, tc.allow)
			}
		})
	}
}

func TestCheckCommand_EmptyAllowlistDeniesAll(t *testing.T) {
	for _, allow := range [][]string{nil, {}, {"", "  "}, {"bad; entry"}} {
		err := CheckCommand("go test ./...", allow, t.TempDir())
		if !errors.Is(err, ErrEmptyAllowlist) {
			t.Errorf("allowlist %q: err = %v, want ErrEmptyAllowlist", allow, err)
		}
	}
}

func TestCheckCommand_NoWorkDirRejectsAnyDotDotAndAbsolute(t *testing.T) {
	allow := []string{"cat", "make"}
	for _, cmd := range []string{"cat ../x", "cat /work/x", "make -f /work/x.mk", "cat sub/../x"} {
		if CheckCommand(cmd, allow, "") == nil {
			t.Errorf("%q must be rejected when no worktree is known", cmd)
		}
	}
	if err := CheckCommand("cat sub/x", allow, ""); err != nil {
		t.Errorf("relative path must be allowed: %v", err)
	}
}

func TestCheckCommand_ErrorMessages(t *testing.T) {
	work := t.TempDir()
	cases := map[string]string{
		"go test -exec sh":            "executes arbitrary programs",
		"make -f":                     "missing its path value",
		"FOO=1 make":                  "environment variable prefix",
		"cat ~/x":                     "home-directory expansion",
		"cat /etc/passwd":             "absolute path outside the worktree",
		"cat ../x":                    "escapes the worktree",
		"go build -linkmode=external": "external linker",
		"go run x":                    "does not match any allowlist entry",
	}
	allow := []string{"go test", "go build", "make", "cat"}
	for cmd, want := range cases {
		err := CheckCommand(cmd, allow, work)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckCommand(%q) err = %v, want containing %q", cmd, err, want)
		}
	}
}

// The legacy two-argument wrapper used by gemma.go must keep its semantics.
func TestIsCommandAllowed_LegacyWrapper(t *testing.T) {
	if !isCommandAllowed("echo hello world", []string{"echo"}) {
		t.Error("echo hello world should be allowed")
	}
	if isCommandAllowed("echo /etc/passwd", []string{"echo"}) {
		t.Error("absolute path must be rejected without a worktree")
	}
	if isCommandAllowed("anything", nil) {
		t.Error("empty allowlist must deny")
	}
}
