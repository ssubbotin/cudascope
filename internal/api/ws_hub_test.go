package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

// A client that reads steadily but slower than snapshots are produced keeps
// its queue full without ever exceeding the write deadline on any single
// frame, so it is never evicted. Dropping the newest snapshot would leave it
// rendering data a whole queue behind, forever; it has to converge on the
// present instead.
func TestSlowClientConvergesOnTheNewestSnapshot(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer srv.Close()

	conn := dialHub(t, srv)
	defer conn.Close()
	waitForClients(t, hub, 1, 2*time.Second)

	// Produce far more than the queue holds while the client reads nothing.
	const last = 200
	snap := bigSnapshot()
	for i := 1; i <= last; i++ {
		snap.Timestamp = int64(i)
		hub.Broadcast(snap)
	}

	// Now let it catch up: the newest snapshot must still be on its way.
	for i := 0; i < 4*wsSendQueue; i++ {
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, data, err := conn.ReadMessage()
		if errors.Is(err, os.ErrDeadlineExceeded) {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		var got collector.Snapshot
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Timestamp == last {
			return
		}
	}

	t.Errorf("never received snapshot %d: the client is stuck behind a full queue", last)
}
