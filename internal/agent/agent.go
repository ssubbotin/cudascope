package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// defaultQueueLimit is how many payloads an agent holds while the hub is
// unreachable. At one GPU batch a second plus host and vLLM samples, this is
// several minutes of outage, for a few hundred kilobytes of memory.
const defaultQueueLimit = 600

// Options configures how an agent talks to its hub.
type Options struct {
	// Token is the shared secret a protected hub expects.
	Token string

	// Auth is "user:password", used when the hub has no ingest token and
	// protects everything with basic auth instead.
	Auth string

	// QueueLimit bounds the buffered payloads. Zero selects the default.
	QueueLimit int
}

// pending is one payload waiting to reach the hub.
type pending struct {
	path string
	body []byte
}

// Agent pushes collected metrics to the hub.
type Agent struct {
	hubURL string
	nodeID string
	client *http.Client

	token string
	user  string
	pass  string

	// mu guards the queue only. It is never held across a network call, so
	// one source's slow POST cannot stall another's tick.
	mu    sync.Mutex
	queue []pending
	limit int

	// sendMu admits one sender at a time. A source that finds it taken has
	// already left its payload in the queue for whoever holds it.
	sendMu sync.Mutex

	dropping bool // whether the queue is currently shedding, logged on change
}

// New creates an Agent that pushes metrics to the given hub URL.
func New(hubURL, nodeID string, opts Options) *Agent {
	a := &Agent{
		hubURL: hubURL,
		nodeID: nodeID,
		client: &http.Client{Timeout: 10 * time.Second},
		token:  opts.Token,
		limit:  opts.QueueLimit,
	}
	if a.limit <= 0 {
		a.limit = defaultQueueLimit
	}
	if opts.Auth != "" {
		a.user, a.pass, _ = strings.Cut(opts.Auth, ":")
	}
	return a
}

// Register sends device info and node registration to the hub.
// Retries until successful or context cancelled.
func (a *Agent) Register(ctx context.Context, devices []collector.GPUDevice) error {
	payload := struct {
		NodeID   string                `json:"node_id"`
		Hostname string                `json:"hostname"`
		Devices  []collector.GPUDevice `json:"devices"`
	}{
		NodeID:   a.nodeID,
		Hostname: a.nodeID,
		Devices:  devices,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	for {
		err := a.send(pending{path: "/api/v1/ingest/register", body: body})
		if err == nil {
			log.Printf("registered with hub at %s (node=%s, gpus=%d)", a.hubURL, a.nodeID, len(devices))
			return nil
		}
		log.Printf("failed to register with hub: %v (retrying in 5s)", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// WriteGPUMetrics implements collector.MetricSink.
func (a *Agent) WriteGPUMetrics(metrics []collector.GPUMetrics) error {
	for i := range metrics {
		metrics[i].NodeID = a.nodeID
	}
	return a.post("/api/v1/ingest/gpu-metrics", metrics)
}

// WriteHostMetrics implements collector.MetricSink.
func (a *Agent) WriteHostMetrics(m *collector.HostMetrics) error {
	m.NodeID = a.nodeID
	return a.post("/api/v1/ingest/host-metrics", m)
}

// WriteGPUProcesses implements collector.MetricSink.
func (a *Agent) WriteGPUProcesses(procs []collector.GPUProcess) error {
	if len(procs) == 0 {
		return nil
	}
	for i := range procs {
		procs[i].NodeID = a.nodeID
	}
	return a.post("/api/v1/ingest/gpu-processes", procs)
}

// WriteVLLMMetrics implements collector.MetricSink.
func (a *Agent) WriteVLLMMetrics(m *collector.VLLMMetrics) error {
	if m == nil {
		return nil
	}
	m.NodeID = a.nodeID
	return a.post("/api/v1/ingest/vllm-metrics", m)
}

// WriteXid reports a driver fault to the hub, where the thresholds and the
// journal live.
func (a *Agent) WriteXid(gpuID int, xid uint64) error {
	return a.post("/api/v1/ingest/xid", map[string]any{
		"node_id": a.nodeID,
		"gpu_id":  gpuID,
		"xid":     xid,
	})
}

// post queues a payload and tries to drain the queue.
//
// Queuing before sending is what keeps a hub restart from punching a hole in
// every agent's history: the payload waits instead of going to the log as an
// error and disappearing.
func (a *Agent) post(path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	a.enqueue(pending{path: path, body: body})
	return a.flush()
}

func (a *Agent) enqueue(p pending) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.queue = append(a.queue, p)
	if len(a.queue) <= a.limit {
		if a.dropping {
			a.dropping = false
			log.Printf("hub reachable again, buffer no longer shedding")
		}
		return
	}

	// The oldest go first: a dashboard catching up wants the recent past,
	// and the alternative is refusing to accept anything new.
	drop := len(a.queue) - a.limit
	a.queue = append(a.queue[:0], a.queue[drop:]...)
	if !a.dropping {
		a.dropping = true
		log.Printf("hub unreachable, buffer full at %d payloads, dropping the oldest", a.limit)
	}
}

// flush sends queued payloads oldest first, stopping at the first failure so
// order is kept. Only one flush runs at a time.
func (a *Agent) flush() error {
	if !a.sendMu.TryLock() {
		// Another source is already draining, and ours is in the queue.
		return nil
	}
	defer a.sendMu.Unlock()

	for {
		item, ok := a.head()
		if !ok {
			return nil
		}
		if err := a.send(item); err != nil {
			return err
		}
		a.dropHead()
	}
}

func (a *Agent) head() (pending, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return pending{}, false
	}
	return a.queue[0], true
}

func (a *Agent) dropHead() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) > 0 {
		a.queue = a.queue[1:]
	}
}

// Pending reports how many payloads are waiting for the hub.
func (a *Agent) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queue)
}

func (a *Agent) send(p pending) error {
	req, err := http.NewRequest(http.MethodPost, a.hubURL+p.path, bytes.NewReader(p.body))
	if err != nil {
		return fmt.Errorf("POST %s: %w", p.path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	switch {
	case a.token != "":
		req.Header.Set("Authorization", "Bearer "+a.token)
	case a.user != "":
		req.SetBasicAuth(a.user, a.pass)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", p.path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: status %d", p.path, resp.StatusCode)
	}
	return nil
}
