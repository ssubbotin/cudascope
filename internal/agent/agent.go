package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// Agent pushes collected metrics to the hub.
type Agent struct {
	hubURL string
	nodeID string
	client *http.Client
}

// New creates a new Agent that pushes metrics to the given hub URL.
func New(hubURL, nodeID string) *Agent {
	return &Agent{
		hubURL: hubURL,
		nodeID: nodeID,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Register sends device info and node registration to the hub.
// Retries until successful or context cancelled.
func (a *Agent) Register(ctx context.Context, devices []collector.GPUDevice) error {
	payload := struct {
		NodeID   string              `json:"node_id"`
		Hostname string              `json:"hostname"`
		Devices  []collector.GPUDevice `json:"devices"`
	}{
		NodeID:   a.nodeID,
		Hostname: a.nodeID,
		Devices:  devices,
	}

	for {
		err := a.post("/api/v1/ingest/register", payload)
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

func (a *Agent) post(path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := a.hubURL + path
	resp, err := a.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	return nil
}
