package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ollamaVersionRetry bounds how often the version is asked for again while
// it is still unknown.
const ollamaVersionRetry = time.Minute

// OllamaCollector reads what an ollama server is holding in memory.
//
// Ollama has no Prometheus endpoint: the request has been open since March
// 2024 (ollama/ollama#3144) and every existing exporter gets its numbers by
// standing in the request path, which is a place a monitor has no business
// being. What ollama does publish is /api/ps, the state of the moment, and
// that is what this reads.
type OllamaCollector struct {
	baseURL string
	nodeID  string
	client  *http.Client

	// version is sticky, the way the vLLM model name is: a scrape that
	// cannot answer leaves the last known value alone.
	version      string
	versionRetry time.Time
}

// NewOllamaCollector creates a collector for an ollama server.
func NewOllamaCollector(baseURL, nodeID string) *OllamaCollector {
	return &OllamaCollector{
		baseURL: strings.TrimRight(baseURL, "/"),
		nodeID:  nodeID,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// psResponse is the part of /api/ps this needs.
type psResponse struct {
	Models []struct {
		Name          string    `json:"name"`
		Model         string    `json:"model"`
		Size          int64     `json:"size"`
		SizeVRAM      int64     `json:"size_vram"`
		ContextLength int64     `json:"context_length"`
		ExpiresAt     time.Time `json:"expires_at"`
	} `json:"models"`
}

// Collect reads the loaded model list. An ollama server with nothing loaded
// answers with an empty list, which is a reading in itself: it says the
// card is free and the next request will pay for a load.
func (c *OllamaCollector) Collect() (*OllamaMetrics, error) {
	var ps psResponse
	if err := c.get("/api/ps", &ps); err != nil {
		return nil, err
	}

	m := &OllamaMetrics{
		NodeID:    c.nodeID,
		Timestamp: time.Now().Unix(),
		Version:   c.resolveVersion(),
		Models:    make([]OllamaModel, 0, len(ps.Models)),
	}

	for _, model := range ps.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}

		var expires int64
		if !model.ExpiresAt.IsZero() {
			expires = model.ExpiresAt.Unix()
		}

		m.Models = append(m.Models, OllamaModel{
			Name:          name,
			SizeBytes:     model.Size,
			VRAMBytes:     model.SizeVRAM,
			ContextLength: model.ContextLength,
			ExpiresAt:     expires,
		})
	}

	return m, nil
}

// resolveVersion asks for the server version once and then leaves it alone.
// It is a label rather than a measurement, and a server that answers /api/ps
// while failing /api/version is still worth reporting on.
func (c *OllamaCollector) resolveVersion() string {
	if c.version != "" || time.Now().Before(c.versionRetry) {
		return c.version
	}
	c.versionRetry = time.Now().Add(ollamaVersionRetry)

	var v struct {
		Version string `json:"version"`
	}
	if err := c.get("/api/version", &v); err != nil {
		log.Printf("ollama: version unavailable: %v", err)
		return c.version
	}
	if v.Version != "" {
		log.Printf("ollama: version = %s", v.Version)
		c.version = v.Version
	}
	return c.version
}

func (c *OllamaCollector) get(path string, into any) error {
	resp, err := c.client.Get(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("get %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: status %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
