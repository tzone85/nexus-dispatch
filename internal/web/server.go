// internal/web/server.go
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	eventStore   state.EventStore
	projStore    *state.SQLiteStore
	hub          *Hub
	port         int
	reqFilter    state.ReqFilter
	httpServer   *http.Server
	metricsCache *MetricsCache
	mempalace    *memory.MemPalace
	dagExport    *graph.DAGExport
	authToken    string    // C1: required as ?token=<hex> on / and /ws
	startedAt    time.Time // for /healthz uptime
	stateDir     string    // for loading improvements.json + future per-state files

	// bindAddrMu guards bindAddr, written by Start() once the listener is up
	// and read concurrently by BindAddr() (e.g., test harnesses asserting
	// the actual port). Without the mutex the -race detector flags it as a
	// data race even though the field only flips once.
	bindAddrMu sync.RWMutex
	bindAddr   string // C1: actual host:port for tightening Origin checks
}

func NewServer(es state.EventStore, ps *state.SQLiteStore, port int, filter state.ReqFilter, stateDir string, mp *memory.MemPalace) *Server {
	var mc *MetricsCache
	if stateDir != "" {
		mc = NewMetricsCache(stateDir)
	}

	// C1: generate a random per-session token. Required on every WebSocket
	// upgrade and on the static-asset HTTP handlers, preventing localhost
	// cross-process CSRF and unauthenticated dashboard access.
	//
	// crypto/rand failures are extremely rare on a healthy host but signal
	// OS entropy starvation — the previous fallback to a nanosecond
	// timestamp gave ~30 bits of entropy and was bruteforceable in
	// milliseconds. Fail fast instead so the operator notices.
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Fatalf("[web] crypto/rand failed (OS entropy starvation?): %v", err)
	}
	token := hex.EncodeToString(tokenBytes)

	s := &Server{
		eventStore:   es,
		projStore:    ps,
		port:         port,
		reqFilter:    filter,
		metricsCache: mc,
		mempalace:    mp,
		authToken:    token,
		startedAt:    time.Now(),
		stateDir:     stateDir,
	}
	s.hub = NewHub(s)
	return s
}

// AuthToken returns the random token required by /ws and asset routes.
func (s *Server) AuthToken() string { return s.authToken }

// BindAddr returns the actual host:port the server listens on, for use
// when tightening WebSocket Origin checks. Safe to call concurrently with
// Start(); returns the zero string until Start() has acquired the listener.
func (s *Server) BindAddr() string {
	s.bindAddrMu.RLock()
	defer s.bindAddrMu.RUnlock()
	return s.bindAddr
}

// checkAuth verifies the request carries the expected ?token=<hex>. Uses
// constant-time comparison to avoid token-length / equality timing leaks.
func (s *Server) checkAuth(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) == 1
}

// SetDAG sets the DAG export for inclusion in state snapshots.
func (s *Server) SetDAG(dag *graph.DAGExport) {
	s.dagExport = dag
}

// Hub returns the WebSocket hub for event bus wiring.
func (s *Server) Hub() *Hub {
	return s.hub
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Serve static files (auth-gated by token).
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("static files: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// C1: index page bootstrap allowed without token (the user pastes the
		// authed URL); subsequent requests with cookies/query carry the token.
		// We require token for everything except the document root, which
		// itself contains the token in its query and seeds it into JS.
		if r.URL.Path == "/" && r.URL.Query().Get("token") == "" {
			http.Error(w, "missing ?token= — see CLI output for the dashboard URL", http.StatusUnauthorized)
			return
		}
		if !s.checkAuth(r) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		// M8: defense-in-depth headers.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws://localhost:* ws://127.0.0.1:*")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		fileServer.ServeHTTP(w, r)
	})

	// WebSocket endpoint — explicit auth check before upgrade.
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAuth(r) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		s.hub.HandleWebSocket(w, r)
	})

	// Health endpoints. Both unauthenticated and inexpensive — designed for
	// container/k8s liveness + readiness probes and external load balancers.
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	addr := fmt.Sprintf("localhost:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use. Try: nxd dashboard --web --port %d", s.port, s.port+1)
	}
	s.bindAddrMu.Lock()
	s.bindAddr = addr
	s.bindAddrMu.Unlock()

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // S3-7-adjacent: prevent slowloris
	}

	// Open browser with the auth-gated URL. The URL contains the token —
	// don't log the bare token separately so it doesn't end up duplicated
	// in log aggregators with a wider read surface than the operator
	// console.
	url := fmt.Sprintf("http://%s/?token=%s", addr, s.authToken)
	log.Printf("Dashboard server running at %s", url)
	openBrowser(url)

	// Start hub broadcast loop
	go s.hub.Run(ctx)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("[web] shutdown: %v", err)
		}
	}()

	return s.httpServer.Serve(listener)
}

func openBrowser(url string) {
	// F6: The URL embeds ?token=<hex>. `open` / `xdg-open` receive it as
	// argv, and `ps` exposes the full argv to every other user on most
	// systems — so auto-launching the browser leaks the dashboard token
	// to every local process in multi-tenant or shared-dev environments.
	//
	// Default policy is now to NOT launch the browser. The operator sees
	// the full token URL via log.Printf and clicks it from their terminal,
	// which keeps the secret off the process table. Set
	// NXD_OPEN_BROWSER=1 to opt back in to the old behaviour on a
	// trusted single-user machine.
	if os.Getenv("NXD_OPEN_BROWSER") != "1" {
		return
	}
	// NXD_NO_BROWSER stays honoured as an explicit suppression toggle
	// (used by some test harnesses) even when NXD_OPEN_BROWSER=1.
	if os.Getenv("NXD_NO_BROWSER") != "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	cmd.Start() //nolint:errcheck
}
