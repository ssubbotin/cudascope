package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// fakeHub records what reached it and can be made to fail at will.
type fakeHub struct {
	srv *httptest.Server

	failing atomic.Bool
	block   chan struct{} // when non-nil, handlers wait on it

	mu       sync.Mutex
	received [][]collector.GPUMetrics
	tokens   []string
	users    []string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{}

	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.block != nil {
			<-h.block
		}
		if h.failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		body, _ := io.ReadAll(r.Body)
		user, _, _ := r.BasicAuth()

		h.mu.Lock()
		h.tokens = append(h.tokens, r.Header.Get("Authorization"))
		h.users = append(h.users, user)
		if r.URL.Path == "/api/v1/ingest/gpu-metrics" {
			var batch []collector.GPUMetrics
			if err := json.Unmarshal(body, &batch); err == nil {
				h.received = append(h.received, batch)
			}
		}
		h.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) batches() [][]collector.GPUMetrics {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]collector.GPUMetrics, len(h.received))
	copy(out, h.received)
	return out
}

func (h *fakeHub) lastAuthorization() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.tokens) == 0 {
		return ""
	}
	return h.tokens[len(h.tokens)-1]
}

func sample(gpuID int, temp int) []collector.GPUMetrics {
	return []collector.GPUMetrics{{GPUID: gpuID, Temperature: temp, Timestamp: time.Now().Unix()}}
}

// A hub restart used to punch a hole in every agent's history: the POST
// failed, the error went to the log, and the sample was gone.
func TestSamplesAreResentAfterTheHubComesBack(t *testing.T) {
	hub := newFakeHub(t)
	a := New(hub.srv.URL, "gpu-node-1", Options{})

	hub.failing.Store(true)
	for i := 0; i < 3; i++ {
		if err := a.WriteGPUMetrics(sample(i, 60+i)); err == nil {
			t.Fatal("a failing hub reported success")
		}
	}
	if got := len(hub.batches()); got != 0 {
		t.Fatalf("the failing hub stored %d batches", got)
	}

	hub.failing.Store(false)
	if err := a.WriteGPUMetrics(sample(3, 70)); err != nil {
		t.Fatalf("send after recovery: %v", err)
	}

	batches := hub.batches()
	if len(batches) != 4 {
		t.Fatalf("want the three buffered batches and the new one, got %d", len(batches))
	}
	for i, batch := range batches {
		if batch[0].GPUID != i {
			t.Fatalf("batch %d out of order: %+v", i, batch)
		}
	}
}

// The buffer is a bounded amount of memory on a GPU node, so a hub that
// stays down must cost the oldest samples rather than the process.
func TestTheBufferDropsTheOldestWhenItIsFull(t *testing.T) {
	hub := newFakeHub(t)
	a := New(hub.srv.URL, "gpu-node-1", Options{QueueLimit: 2})

	hub.failing.Store(true)
	for i := 0; i < 5; i++ {
		a.WriteGPUMetrics(sample(i, 60))
	}

	hub.failing.Store(false)
	if err := a.WriteGPUMetrics(sample(9, 60)); err != nil {
		t.Fatalf("send after recovery: %v", err)
	}

	// The limit counts the new payload too, so queuing GPU 9 pushed GPU 3
	// out and the hub sees the newest two of everything.
	batches := hub.batches()
	if len(batches) != 2 {
		t.Fatalf("want the buffer capped at 2 payloads, got %d", len(batches))
	}
	if batches[0][0].GPUID != 4 || batches[1][0].GPUID != 9 {
		t.Fatalf("want the newest samples kept, got %+v", batches)
	}
}

// Each metric source runs on its own goroutine precisely so a slow one
// cannot stop the others. A single send lock held across the network would
// hand that property back.
func TestASlowSendDoesNotHoldUpAnotherSource(t *testing.T) {
	hub := newFakeHub(t)
	hub.block = make(chan struct{})
	a := New(hub.srv.URL, "gpu-node-1", Options{})

	started := make(chan struct{})
	go func() {
		close(started)
		a.WriteGPUMetrics(sample(0, 60))
	}()
	<-started
	time.Sleep(50 * time.Millisecond) // let the first send reach the server

	done := make(chan struct{})
	go func() {
		a.WriteHostMetrics(&collector.HostMetrics{NodeID: "gpu-node-1", Timestamp: time.Now().Unix()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second source waited for the first one's network call")
	}

	close(hub.block)
}

func TestTheTokenIsPresentedToTheHub(t *testing.T) {
	hub := newFakeHub(t)
	a := New(hub.srv.URL, "gpu-node-1", Options{Token: "s3cret"})

	if err := a.WriteGPUMetrics(sample(0, 60)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := hub.lastAuthorization(); got != "Bearer s3cret" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestBasicCredentialsAreUsedWhenNoTokenIsSet(t *testing.T) {
	hub := newFakeHub(t)
	a := New(hub.srv.URL, "gpu-node-1", Options{Auth: "admin:hunter2"})

	if err := a.WriteGPUMetrics(sample(0, 60)); err != nil {
		t.Fatalf("write: %v", err)
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.users) == 0 || hub.users[len(hub.users)-1] != "admin" {
		t.Fatalf("basic credentials were not sent: %+v", hub.users)
	}
}
