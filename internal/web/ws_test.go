package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestHub_AddRemoveClient(t *testing.T) {
	s := newTestServer(t)

	// Create a real WebSocket connection via httptest
	handler := s.hub.server
	_ = handler // hub already has reference

	// Use direct hub manipulation - addClient/removeClient take *websocket.Conn
	// We need a real connection to test these methods
	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Read the initial state message
	var initResp WSResponse
	if err := wsjson.Read(ctx, conn, &initResp); err != nil {
		t.Fatalf("Read init: %v", err)
	}
	if initResp.Type != "state" {
		t.Errorf("expected state init message, got %q", initResp.Type)
	}

	// Verify client was added
	s.hub.mu.Lock()
	clientCount := len(s.hub.clients)
	s.hub.mu.Unlock()

	if clientCount != 1 {
		t.Errorf("expected 1 client after connect, got %d", clientCount)
	}

	// Close the connection
	conn.Close(websocket.StatusNormalClosure, "done")

	// Give server time to process the close
	time.Sleep(100 * time.Millisecond)

	// Verify client was removed
	s.hub.mu.Lock()
	clientCount = len(s.hub.clients)
	s.hub.mu.Unlock()

	if clientCount != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", clientCount)
	}
}

func TestHub_HandleWebSocket_SendsInitialState(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	seedStory(t, s, reqID)

	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	var resp WSResponse
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp.Type != "state" {
		t.Errorf("expected state message, got %q", resp.Type)
	}
	if resp.Data == nil {
		t.Error("expected non-nil Data in state message")
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestHub_HandleWebSocket_CommandResponse(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial state
	var initResp WSResponse
	wsjson.Read(ctx, conn, &initResp) //nolint:errcheck

	// Send a command
	msg := WSMessage{
		Type:   "command",
		Action: "pause_requirement",
	}
	// Marshal the payload
	payloadBytes := mustMarshal(t, map[string]any{"req_id": reqID})
	msg.Payload = payloadBytes

	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read the command response
	var cmdResp WSResponse
	if err := wsjson.Read(ctx, conn, &cmdResp); err != nil {
		t.Fatalf("Read command response: %v", err)
	}

	if cmdResp.Type != "command_result" {
		t.Errorf("expected command_result, got %q", cmdResp.Type)
	}
	if !cmdResp.Success {
		t.Errorf("expected success, got message: %s", cmdResp.Message)
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestHub_Run_BroadcastsState(t *testing.T) {
	s := newTestServer(t)
	seedRequirement(t, s)

	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial state
	var initResp WSResponse
	wsjson.Read(ctx, conn, &initResp) //nolint:errcheck

	// Start the hub Run loop
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go s.hub.Run(runCtx)

	// Wait for a broadcast cycle (Hub ticks every 2s)
	var broadcastResp WSResponse
	if err := wsjson.Read(ctx, conn, &broadcastResp); err != nil {
		t.Fatalf("Read broadcast: %v", err)
	}

	if broadcastResp.Type != "state" {
		t.Errorf("expected state broadcast, got %q", broadcastResp.Type)
	}

	runCancel()
	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestHub_Broadcast_EventDiff(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial state
	var initResp WSResponse
	wsjson.Read(ctx, conn, &initResp) //nolint:errcheck

	// Set lastEventCount so broadcast will detect new events
	s.hub.lastEventCount = 1

	// Add a new event
	evt := state.NewEvent(state.EventStoryCreated, "system", "story-new", map[string]any{
		"id":                  "story-new",
		"req_id":              reqID,
		"title":               "New Story",
		"description":         "Test",
		"acceptance_criteria": "Works",
		"complexity":          1,
	})
	s.eventStore.Append(evt)  //nolint:errcheck
	s.projStore.Project(evt)  //nolint:errcheck

	// Manually trigger broadcast
	s.hub.broadcast(ctx)

	// We should receive event diffs and then a state message
	var resp WSResponse
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	// First message should be an event diff
	if resp.Type != "event" {
		t.Errorf("expected event message, got %q", resp.Type)
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestHub_CloseAll(t *testing.T) {
	s := newTestServer(t)

	// Use a single connection to test closeAll — avoids multi-connection timeout issues
	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read init message
	var resp WSResponse
	wsjson.Read(ctx, conn, &resp) //nolint:errcheck

	// Wait for client to be registered
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		s.hub.mu.Lock()
		count := len(s.hub.clients)
		s.hub.mu.Unlock()
		if count >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.hub.mu.Lock()
	countBefore := len(s.hub.clients)
	s.hub.mu.Unlock()
	if countBefore < 1 {
		t.Fatalf("expected at least 1 client before closeAll, got %d", countBefore)
	}

	// closeAll should remove all clients from the map
	s.hub.closeAll()

	s.hub.mu.Lock()
	countAfter := len(s.hub.clients)
	s.hub.mu.Unlock()

	if countAfter != 0 {
		t.Errorf("expected 0 clients after closeAll, got %d", countAfter)
	}
}

func TestHub_Broadcast_SendsStateToClients(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	seedStory(t, s, reqID)

	mux := setupTestWSServer(t, s)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read the initial state sent by HandleWebSocket
	var resp WSResponse
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp.Type != "state" {
		t.Errorf("expected state, got %q", resp.Type)
	}

	// Trigger a broadcast which internally calls sendState logic
	s.hub.broadcast(ctx)

	// Read the state message from broadcast
	var resp2 WSResponse
	if err := wsjson.Read(ctx, conn, &resp2); err != nil {
		t.Fatalf("Read broadcast state: %v", err)
	}

	if resp2.Type != "state" {
		t.Errorf("expected state from broadcast, got %q", resp2.Type)
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestHub_Broadcast_NoClientsNoError(t *testing.T) {
	s := newTestServer(t)

	// broadcast with no clients should not panic
	ctx := context.Background()
	s.hub.broadcast(ctx)
}

func TestServer_Start_GracefulShutdown(t *testing.T) {
	s := newTestServer(t)

	// Use a high port to avoid conflicts
	s.port = 0 // will cause net.Listen with "localhost:0" to pick a random port

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Give the server time to start
	time.Sleep(200 * time.Millisecond)

	// Cancel context to trigger graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		// http.ErrServerClosed is the expected result
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("expected http.ErrServerClosed, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

func TestServer_Start_PortConflict(t *testing.T) {
	s := newTestServer(t)
	s.port = 0

	// Start first server to occupy the port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Start(ctx) //nolint:errcheck

	// Give first server time to bind
	time.Sleep(200 * time.Millisecond)

	// Start second server on same port — we need to get the actual port from httpServer
	// Instead, bind a listener ourselves on a known port and then try Start on that port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	s2 := newTestServer(t)
	s2.port = port

	// The listener is still held, so Start should fail
	err = s2.Start(context.Background())
	listener.Close() // clean up
	if err == nil {
		t.Error("expected error when port is in use")
	}
}

// setupTestWSServer creates an HTTP test server with the Hub WebSocket handler wired up.
func setupTestWSServer(t *testing.T, s *Server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.hub.HandleWebSocket)
	return mux
}
