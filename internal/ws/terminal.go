package ws

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Hub manages WebSocket connections for streaming analysis output.
type Hub struct {
	mu      sync.RWMutex
	rooms   map[string]map[*websocket.Conn]bool
	history map[string][][]byte // recent messages per room for late joiners
}

const maxHistory = 200

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		rooms:   make(map[string]map[*websocket.Conn]bool),
		history: make(map[string][][]byte),
	}
}

// HandleConnect upgrades an HTTP request to WebSocket and subscribes to
// an analysis room.
func (h *Hub) HandleConnect(
	w http.ResponseWriter,
	r *http.Request,
	analysisID string,
) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket upgrade failed")
		return
	}

	h.mu.Lock()
	if h.rooms[analysisID] == nil {
		h.rooms[analysisID] = make(map[*websocket.Conn]bool)
	}
	h.rooms[analysisID][conn] = true
	// Send buffered history to the new client.
	for _, msg := range h.history[analysisID] {
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	}
	h.mu.Unlock()

	log.Info().
		Str("analysis_id", analysisID).
		Msg("WebSocket client connected")

	go func() {
		defer func() {
			h.removeConn(analysisID, conn)
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// Broadcast sends a message to all clients subscribed to an analysis.
func (h *Hub) Broadcast(analysisID string, data []byte) {
	h.mu.Lock()
	// Append to history buffer.
	buf := make([]byte, len(data))
	copy(buf, data)
	hist := h.history[analysisID]
	if len(hist) >= maxHistory {
		hist = hist[len(hist)-maxHistory+1:]
	}
	h.history[analysisID] = append(hist, buf)
	conns := h.rooms[analysisID]
	h.mu.Unlock()

	for conn := range conns {
		if err := conn.WriteMessage(
			websocket.TextMessage,
			data,
		); err != nil {
			h.removeConn(analysisID, conn)
			conn.Close()
		}
	}
}

// CloseRoom disconnects all clients from an analysis room and removes it.
func (h *Hub) CloseRoom(analysisID string) {
	h.mu.Lock()
	conns := h.rooms[analysisID]
	delete(h.rooms, analysisID)
	delete(h.history, analysisID)
	h.mu.Unlock()

	for conn := range conns {
		_ = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(
				websocket.CloseNormalClosure,
				"analysis complete",
			),
		)
		_ = conn.Close()
	}
}

func (h *Hub) removeConn(analysisID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room := h.rooms[analysisID]; room != nil {
		delete(room, conn)
		if len(room) == 0 {
			delete(h.rooms, analysisID)
		}
	}
}
