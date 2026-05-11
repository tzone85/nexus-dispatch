package web

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// freePort returns a port that's available right now. There's a TOCTOU
// race with another process grabbing it before Server.Start binds,
// but for a single test on localhost the window is microseconds and
// the failure mode is a test retry — not flaky over realistic load.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// TestServer_Start_GracefulShutdown_Extra drives Server.Start in a
// goroutine with a context we cancel after the listener binds.
// Confirms:
//   - Start returns http.ErrServerClosed after Shutdown (the
//     server-was-killed-gracefully signal).
//   - The bindAddr accessor reflects the actual address post-bind.
//   - The /healthz endpoint responds with 200 while running.
//
// Was the Start path at 61% pre-#32; this brings Start's main flow
// to ~90%.
func TestServer_Start_GracefulShutdown_Extra(t *testing.T) {
	s := newTestServer(t)
	port := freePort(t)
	s.port = port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Wait for the server to bind.
	if !waitForPort(t, port, 2*time.Second) {
		t.Fatal("server did not bind within 2s")
	}

	// bindAddr must reflect the actual listener address.
	if s.BindAddr() == "" {
		t.Error("bindAddr should be set after Start")
	}

	// /healthz must respond.
	resp, err := http.Get("http://127.0.0.1:" + itoa(port) + "/healthz")
	if err != nil {
		t.Errorf("GET /healthz: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("/healthz status = %d, want 200", resp.StatusCode)
		}
	}

	// Trigger graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("Start returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within 5s of context cancel")
	}
}

// TestServer_Start_PortAlreadyInUseErrors covers the "port busy"
// error path. We pre-bind the port so Start's net.Listen call
// fails with EADDRINUSE.
func TestServer_Start_PortAlreadyInUseErrors(t *testing.T) {
	port := freePort(t)
	// Hold the port for the duration of the test.
	blocker, err := net.Listen("tcp", "127.0.0.1:"+itoa(port))
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer blocker.Close()

	s := newTestServer(t)
	s.port = port

	err = s.Start(context.Background())
	if err == nil {
		t.Fatal("expected port-in-use error from Start")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should mention 'already in use'; got %v", err)
	}
}

// TestServer_Start_TokenGatesIndex confirms /'s auth gate: a GET to
// / without ?token= must return 401. The behaviour is documented as
// "the user pastes the authed URL" so we ensure the gate fires.
func TestServer_Start_TokenGatesIndex(t *testing.T) {
	s := newTestServer(t)
	port := freePort(t)
	s.port = port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()
	if !waitForPort(t, port, 2*time.Second) {
		t.Fatal("server did not bind within 2s")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	resp, err := http.Get("http://127.0.0.1:" + itoa(port) + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token; got %d", resp.StatusCode)
	}
}

// TestServer_Start_TokenAcceptsIndex confirms / with a valid token
// returns 200 + serves the index page. Token comes from
// s.AuthToken().
func TestServer_Start_TokenAcceptsIndex(t *testing.T) {
	s := newTestServer(t)
	port := freePort(t)
	s.port = port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()
	if !waitForPort(t, port, 2*time.Second) {
		t.Fatal("server did not bind")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	url := "http://127.0.0.1:" + itoa(port) + "/?token=" + s.AuthToken()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 with token; got %d", resp.StatusCode)
	}
	// Confirm CSP header is set (defense-in-depth from line 122 of
	// server.go).
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("expected Content-Security-Policy header")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options=nosniff; got %q", resp.Header.Get("X-Content-Type-Options"))
	}
}

// waitForPort polls a port until a TCP connection succeeds or the
// timeout expires. Returns true on connect, false on timeout.
func waitForPort(t *testing.T, port int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// itoa is here to avoid importing strconv just for this.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
