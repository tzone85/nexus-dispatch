// internal/web/server.go
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
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
}

func NewServer(es state.EventStore, ps *state.SQLiteStore, port int, filter state.ReqFilter, stateDir string, mp *memory.MemPalace) *Server {
	var mc *MetricsCache
	if stateDir != "" {
		mc = NewMetricsCache(stateDir)
	}

	s := &Server{
		eventStore:   es,
		projStore:    ps,
		port:         port,
		reqFilter:    filter,
		metricsCache: mc,
		mempalace:    mp,
	}
	s.hub = NewHub(s)
	return s
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

	// Serve static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("static files: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.hub.HandleWebSocket)

	addr := fmt.Sprintf("localhost:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use. Try: nxd dashboard --web --port %d", s.port, s.port+1)
	}

	s.httpServer = &http.Server{Handler: mux}

	// Open browser
	url := fmt.Sprintf("http://%s", addr)
	log.Printf("Dashboard server running at %s", url)
	openBrowser(url)

	// Start hub broadcast loop
	go s.hub.Run(ctx)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	return s.httpServer.Serve(listener)
}

func openBrowser(url string) {
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
