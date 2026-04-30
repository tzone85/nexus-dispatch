// internal/web/ws.go
package web

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

type Hub struct {
	server         *Server
	clients        map[*websocket.Conn]bool
	mu             sync.Mutex
	lastEventCount int
	eventBus       *EventBus
}

func NewHub(s *Server) *Hub {
	return &Hub{
		server:  s,
		clients: make(map[*websocket.Conn]bool),
	}
}

// SetEventBus connects the hub to an event bus for instant event delivery.
func (h *Hub) SetEventBus(bus *EventBus) {
	h.eventBus = bus
}

type WSMessage struct {
	Type    string          `json:"type"`
	Action  string          `json:"action,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type WSResponse struct {
	Type    string `json:"type"`
	Action  string `json:"action,omitempty"`
	Success bool   `json:"success,omitempty"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
	// SeqNo is set on type=="event" messages for gap detection.
	SeqNo uint64 `json:"seq_no,omitempty"`
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// H1: pin Origin to the actual bind address rather than allowing any
	// localhost port. Without this, any process on the same machine can
	// open a WebSocket via Origin spoofing.
	origins := []string{"localhost:*", "127.0.0.1:*"}
	if h.server != nil && h.server.BindAddr() != "" {
		origins = []string{h.server.BindAddr()}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: origins,
	})
	if err != nil {
		log.Printf("[ws] accept error: %v", err)
		return
	}
	defer conn.CloseNow()

	h.addClient(conn)
	defer h.removeClient(conn)

	// Send initial state
	h.sendState(r.Context(), conn)

	// Read commands
	for {
		var msg WSMessage
		err := wsjson.Read(r.Context(), conn, &msg)
		if err != nil {
			break
		}
		if msg.Type == "command" {
			result := h.server.HandleCommand(msg.Action, msg.Payload)
			wsjson.Write(r.Context(), conn, result) //nolint:errcheck
		}
	}
}

func (h *Hub) Run(ctx context.Context) {
	// State snapshots are now a safety net (for reconnect and aggregate views).
	// Events are pushed instantly via the event bus when available.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Subscribe to event bus for instant event delivery.
	var eventCh chan EventEnvelope
	if h.eventBus != nil {
		eventCh = h.eventBus.Subscribe()
		defer h.eventBus.Unsubscribe(eventCh)
	}

	for {
		select {
		case <-ctx.Done():
			h.closeAll()
			return
		case env, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			h.pushEvent(ctx, env.Event, env.SeqNo)
		case <-ticker.C:
			h.broadcast(ctx)
		}
	}
}

// pushEvent sends a single event to all connected clients immediately.
// seqNo is the EventBus monotonic sequence number — clients can track gaps.
func (h *Hub) pushEvent(ctx context.Context, evt state.Event, seqNo uint64) {
	msg := WSResponse{Type: "event", SeqNo: seqNo, Data: EventSummary{
		Type:      string(evt.Type),
		Timestamp: evt.Timestamp.Format("15:04:05"),
		AgentID:   evt.AgentID,
		StoryID:   evt.StoryID,
		Payload:   state.DecodePayload(evt.Payload),
	}}

	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		wsjson.Write(ctx, conn, msg) //nolint:errcheck
	}
}

func (h *Hub) broadcast(ctx context.Context) {
	// Event diff: detect and push new events before state snapshot
	currentCount, _ := h.server.eventStore.Count(state.EventFilter{})
	if currentCount > h.lastEventCount && h.lastEventCount > 0 {
		newEvents, _ := h.server.eventStore.List(state.EventFilter{Limit: currentCount - h.lastEventCount})
		for _, evt := range newEvents {
			evtMsg := WSResponse{Type: "event", Data: EventSummary{
				Type:      string(evt.Type),
				Timestamp: evt.Timestamp.Format("15:04:05"),
				AgentID:   evt.AgentID,
				StoryID:   evt.StoryID,
			}}
			h.mu.Lock()
			for conn := range h.clients {
				wsjson.Write(ctx, conn, evtMsg) //nolint:errcheck
			}
			h.mu.Unlock()
		}
	}
	h.lastEventCount = currentCount

	// Full state snapshot
	snap, err := h.server.BuildSnapshot()
	if err != nil {
		return
	}

	msg := WSResponse{Type: "state", Data: snap}

	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.clients {
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			conn.CloseNow()
			delete(h.clients, conn)
		}
	}
}

func (h *Hub) sendState(ctx context.Context, conn *websocket.Conn) {
	snap, err := h.server.BuildSnapshot()
	if err != nil {
		return
	}
	wsjson.Write(ctx, conn, WSResponse{Type: "state", Data: snap}) //nolint:errcheck
}

func (h *Hub) addClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = true
	log.Printf("[ws] client connected (%d total)", len(h.clients))
}

func (h *Hub) removeClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
	log.Printf("[ws] client disconnected (%d remaining)", len(h.clients))
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		conn.Close(websocket.StatusGoingAway, "server shutting down")
		delete(h.clients, conn)
	}
}
