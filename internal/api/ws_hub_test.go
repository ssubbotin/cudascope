package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sergey/cudascope/internal/collector"
)

// dialHub connects a websocket client to a test server running the hub.
func dialHub(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func waitForClients(t *testing.T, h *Hub, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if h.ClientCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d ws client(s), got %d", want, h.ClientCount())
}

// bigSnapshot is large enough that a few hundred of them cannot fit in the
// socket buffers of a client that has stopped reading.
func bigSnapshot() collector.Snapshot {
	gpus := make([]collector.GPUMetrics, 500)
	for i := range gpus {
		gpus[i] = collector.GPUMetrics{NodeID: "local", Timestamp: time.Now().Unix(), GPUID: i, PState: 1}
	}
	return collector.Snapshot{Type: "gpu_metrics", Timestamp: time.Now().Unix(), GPUs: gpus}
}

// Broadcast runs inline on the collector goroutine. A client that stops
// reading must therefore never be able to block it: on 2026-09-16 that is
// exactly what stopped GPU, host and vLLM collection for over an hour while
// the service kept answering HTTP and looked healthy.
func TestBroadcastDoesNotBlockOnStalledClient(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer srv.Close()

	conn := dialHub(t, srv)
	defer conn.Close()
	waitForClients(t, hub, 1, 2*time.Second)

	// The client never calls ReadMessage, so its receive buffers fill up.
	done := make(chan struct{})
	go func() {
		defer close(done)
		snap := bigSnapshot()
		for i := 0; i < 500; i++ {
			hub.Broadcast(snap)
		}
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Broadcast blocked on a client that stopped reading")
	}
}

// A client that cannot keep up must eventually be dropped, otherwise it stays
// in the client map forever and every later broadcast pays for it.
func TestStalledClientIsEvicted(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer srv.Close()

	conn := dialHub(t, srv)
	defer conn.Close()
	waitForClients(t, hub, 1, 2*time.Second)

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		snap := bigSnapshot()
		for {
			select {
			case <-stop:
				return
			default:
				hub.Broadcast(snap)
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	waitForClients(t, hub, 0, 30*time.Second)
}

// The collector now runs one goroutine per metric source, so Broadcast is
// called concurrently. Run under -race.
func TestBroadcastIsSafeFromMultipleGoroutines(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer srv.Close()

	conn := dialHub(t, srv)
	defer conn.Close()
	waitForClients(t, hub, 1, 2*time.Second)

	// A well-behaved client that keeps reading.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := collector.Snapshot{Type: "gpu_metrics", Timestamp: time.Now().Unix()}
			for i := 0; i < 100; i++ {
				hub.Broadcast(snap)
			}
		}()
	}
	wg.Wait()
}
