package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sergey/cudascope/internal/collector"
)

const (
	// wsWriteWait bounds a single frame write. Without a deadline a client
	// that stops reading blocks the writer forever once the socket buffers
	// fill up.
	wsWriteWait = 5 * time.Second

	// wsSendQueue is the per-client outbound queue depth. Snapshots are
	// dropped once it is full, which keeps a slow client from holding up
	// the collector.
	wsSendQueue = 32
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsClient is one connected dashboard. Every write to conn goes through the
// client's own writePump goroutine: gorilla/websocket panics on concurrent
// writes, and the collector broadcasts from several goroutines.
type wsClient struct {
	conn      *websocket.Conn
	send      chan []byte
	writeWait time.Duration

	done     chan struct{}
	doneOnce sync.Once

	dropOnce sync.Once
}

// stop releases the client. Safe to call more than once.
func (c *wsClient) stop() {
	c.doneOnce.Do(func() { close(c.done) })
}

// Hub manages WebSocket clients and broadcasts metric snapshots.
type Hub struct {
	clients map[*wsClient]struct{}
	mu      sync.RWMutex

	// writeWait bounds a single frame write, wsWriteWait unless a test
	// shortens it. Evicting a stalled client is one of the invariants here,
	// and a test of it otherwise has to sit out the whole five seconds.
	writeWait time.Duration
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		clients:   make(map[*wsClient]struct{}),
		writeWait: wsWriteWait,
	}
}

// ClientCount returns the number of connected WebSocket clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// HandleWS upgrades HTTP to WebSocket and registers the client.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	c := &wsClient{
		conn:      conn,
		send:      make(chan []byte, wsSendQueue),
		done:      make(chan struct{}),
		writeWait: h.writeWait,
	}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	total := len(h.clients)
	h.mu.Unlock()

	log.Printf("ws client connected (%d total)", total)

	go c.writePump()

	// Read loop (just to detect disconnect)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	h.remove(c)
}

// remove unregisters a client and tears down its connection.
func (h *Hub) remove(c *wsClient) {
	h.mu.Lock()
	_, present := h.clients[c]
	delete(h.clients, c)
	remaining := len(h.clients)
	h.mu.Unlock()

	c.stop()
	c.conn.Close()

	if present {
		log.Printf("ws client disconnected (%d remaining)", remaining)
	}
}

// writePump is the only goroutine that writes to the client's connection.
func (c *wsClient) writePump() {
	for {
		select {
		case <-c.done:
			return
		case data := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
				c.conn.Close()
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				// Closing unblocks the read loop, which unregisters us.
				c.conn.Close()
				return
			}
		}
	}
}

// Broadcast sends a snapshot to all connected clients. It never blocks: a
// client whose queue is full loses its oldest pending snapshot instead, and
// one whose connection stalls outright is dropped by its own writePump once
// a write exceeds wsWriteWait.
func (h *Hub) Broadcast(snap collector.Snapshot) {
	h.send(snap)
}

// BroadcastState sends a state snapshot to all connected clients, under the
// same never-block rules as a metric snapshot.
func (h *Hub) BroadcastState(snap StateSnapshot) {
	h.send(snap)
}

func (h *Hub) send(msg any) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.clients) == 0 {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws marshal error: %v", err)
		return
	}

	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Make room by discarding the oldest pending snapshot. Dropping
			// the new one instead would leave a client that reads steadily
			// but slower than we produce permanently a full queue behind,
			// with nothing to pull it back to the present.
			select {
			case <-c.send:
			default:
			}
			select {
			case c.send <- data:
			default:
			}

			c.dropOnce.Do(func() {
				log.Printf("ws client too slow, discarding older snapshots")
			})
		}
	}
}
