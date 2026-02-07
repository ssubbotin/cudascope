package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/storage"
)

// Server is the HTTP API server.
type Server struct {
	store   *storage.DB
	hub     *Hub
	mux     *http.ServeMux
	uiFS    fs.FS // embedded or filesystem UI
	devMode bool
	uiDir   string
}

// NewServer creates a new API server.
func NewServer(store *storage.DB, hub *Hub, uiFS fs.FS, devMode bool, uiDir string) *Server {
	s := &Server{
		store:   store,
		hub:     hub,
		mux:     http.NewServeMux(),
		uiFS:    uiFS,
		devMode: devMode,
		uiDir:   uiDir,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Read endpoints
	s.mux.HandleFunc("/api/v1/status", s.handleStatus)
	s.mux.HandleFunc("/api/v1/nodes", s.handleNodes)
	s.mux.HandleFunc("/api/v1/gpus", s.handleGPUs)
	s.mux.HandleFunc("/api/v1/gpus/", s.handleGPURoute)
	s.mux.HandleFunc("/api/v1/host/metrics", s.handleHostMetrics)
	s.mux.HandleFunc("/api/v1/ws", s.hub.HandleWS)
	s.mux.HandleFunc("/api/v1/healthz", s.handleHealthz)

	// Ingest endpoints (for agent -> hub communication)
	s.mux.HandleFunc("/api/v1/ingest/register", s.handleIngestRegister)
	s.mux.HandleFunc("/api/v1/ingest/gpu-metrics", s.handleIngestGPUMetrics)
	s.mux.HandleFunc("/api/v1/ingest/host-metrics", s.handleIngestHostMetrics)
	s.mux.HandleFunc("/api/v1/ingest/gpu-processes", s.handleIngestGPUProcesses)

	// Serve UI
	if s.devMode {
		log.Printf("serving UI from filesystem: %s", s.uiDir)
		s.mux.Handle("/", http.FileServer(http.Dir(s.uiDir)))
	} else if s.uiFS != nil {
		s.mux.Handle("/", s.spaHandler())
	}
}

// spaHandler serves the embedded SPA, falling back to index.html for client-side routing.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.uiFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Try to serve the file directly
		f, err := s.uiFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("HTTP server listening on %s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.corsMiddleware(s.mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// handleNodes returns the list of known nodes with online status.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.GetNodes()
	if err != nil {
		httpError(w, "get nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if nodes == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, nodes)
}

// handleStatus returns the current snapshot of all GPUs, hosts, and nodes.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	nodeFilter := r.URL.Query().Get("node")

	gpus, err := s.store.GetLatestGPUMetrics()
	if err != nil {
		httpError(w, "get gpu metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	hosts, err := s.store.GetLatestHostMetrics()
	if err != nil {
		httpError(w, "get host metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	devices, err := s.store.GetGPUDevices(nodeFilter)
	if err != nil {
		httpError(w, "get devices: "+err.Error(), http.StatusInternalServerError)
		return
	}

	procs, _ := s.store.GetAllGPUProcesses()

	nodes, _ := s.store.GetNodes()

	// Filter by node if specified
	if nodeFilter != "" {
		gpus = filterGPUByNode(gpus, nodeFilter)
		hosts = filterHostByNode(hosts, nodeFilter)
		procs = filterProcByNode(procs, nodeFilter)
	}

	resp := map[string]any{
		"nodes":     nodes,
		"devices":   devices,
		"gpus":      gpus,
		"hosts":     hosts,
		"processes": procs,
	}
	writeJSON(w, resp)
}

// handleGPUs lists GPU devices.
func (s *Server) handleGPUs(w http.ResponseWriter, r *http.Request) {
	nodeFilter := r.URL.Query().Get("node")
	devices, err := s.store.GetGPUDevices(nodeFilter)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if devices == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, devices)
}

// handleGPURoute dispatches /api/v1/gpus/:id/... routes.
func (s *Server) handleGPURoute(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/v1/gpus/{id}/metrics or /api/v1/gpus/{id}/processes
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// api / v1 / gpus / {id} / {action}
	if len(parts) < 5 {
		httpError(w, "invalid path", http.StatusBadRequest)
		return
	}

	gpuID, err := strconv.Atoi(parts[3])
	if err != nil {
		httpError(w, "invalid gpu id", http.StatusBadRequest)
		return
	}

	switch parts[4] {
	case "metrics":
		s.handleGPUMetrics(w, r, gpuID)
	case "processes":
		s.handleGPUProcesses(w, r, gpuID)
	default:
		httpError(w, "unknown action", http.StatusNotFound)
	}
}

func (s *Server) handleGPUMetrics(w http.ResponseWriter, r *http.Request, gpuID int) {
	from, to := parseTimeRange(r)
	nodeID := r.URL.Query().Get("node")

	metrics, err := s.store.GetGPUMetrics(storage.GPUMetricsQuery{
		GPUID:  gpuID,
		NodeID: nodeID,
		From:   from,
		To:     to,
	})
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metrics == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, metrics)
}

func (s *Server) handleGPUProcesses(w http.ResponseWriter, r *http.Request, gpuID int) {
	nodeID := r.URL.Query().Get("node")
	procs, err := s.store.GetGPUProcesses(gpuID, nodeID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if procs == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, procs)
}

func (s *Server) handleHostMetrics(w http.ResponseWriter, r *http.Request) {
	from, to := parseTimeRange(r)
	nodeID := r.URL.Query().Get("node")

	metrics, err := s.store.GetHostMetrics(from, to, nodeID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metrics == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, metrics)
}

// --- Ingest endpoints (agent -> hub) ---

func (s *Server) handleIngestRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		NodeID   string              `json:"node_id"`
		Hostname string              `json:"hostname"`
		Devices  []collector.GPUDevice `json:"devices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if payload.NodeID == "" {
		httpError(w, "node_id required", http.StatusBadRequest)
		return
	}

	// Register node
	if err := s.store.RegisterNode(payload.NodeID, payload.Hostname, len(payload.Devices)); err != nil {
		httpError(w, "register node: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Register devices
	if err := s.store.RegisterGPUDevices(payload.NodeID, payload.Devices); err != nil {
		httpError(w, "register devices: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("agent registered: node=%s gpus=%d", payload.NodeID, len(payload.Devices))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleIngestGPUMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var metrics []collector.GPUMetrics
	if err := json.NewDecoder(r.Body).Decode(&metrics); err != nil {
		httpError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.store.WriteGPUMetrics(metrics); err != nil {
		httpError(w, "write gpu metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update node last_seen and broadcast to WebSocket clients
	if len(metrics) > 0 {
		nodeID := metrics[0].NodeID
		s.store.UpdateNodeSeen(nodeID)

		s.hub.Broadcast(collector.Snapshot{
			Type:      "gpu_metrics",
			NodeID:    nodeID,
			Timestamp: time.Now().Unix(),
			GPUs:      metrics,
		})
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleIngestHostMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var m collector.HostMetrics
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		httpError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.store.WriteHostMetrics(&m); err != nil {
		httpError(w, "write host metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.store.UpdateNodeSeen(m.NodeID)

	s.hub.Broadcast(collector.Snapshot{
		Type:      "host_metrics",
		NodeID:    m.NodeID,
		Timestamp: time.Now().Unix(),
		Host:      &m,
	})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleIngestGPUProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var procs []collector.GPUProcess
	if err := json.NewDecoder(r.Body).Decode(&procs); err != nil {
		httpError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.store.WriteGPUProcesses(procs); err != nil {
		httpError(w, "write gpu processes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(procs) > 0 {
		nodeID := procs[0].NodeID
		s.store.UpdateNodeSeen(nodeID)

		s.hub.Broadcast(collector.Snapshot{
			Type:      "gpu_processes",
			NodeID:    nodeID,
			Timestamp: time.Now().Unix(),
			Processes: procs,
		})
	}

	w.WriteHeader(http.StatusOK)
}

// --- Helpers ---

func parseTimeRange(r *http.Request) (from, to int64) {
	now := time.Now().Unix()
	to = now

	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = t
		}
	}

	from = to - 300 // default: last 5 minutes
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = t
		}
	}

	// Also support ?range=5m, 15m, 1h, 6h, 24h
	if v := r.URL.Query().Get("range"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			from = to - int64(d.Seconds())
		}
	}

	return from, to
}

func filterGPUByNode(metrics []collector.GPUMetrics, nodeID string) []collector.GPUMetrics {
	var filtered []collector.GPUMetrics
	for _, m := range metrics {
		if m.NodeID == nodeID {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func filterHostByNode(metrics []collector.HostMetrics, nodeID string) []collector.HostMetrics {
	var filtered []collector.HostMetrics
	for _, m := range metrics {
		if m.NodeID == nodeID {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func filterProcByNode(procs []collector.GPUProcess, nodeID string) []collector.GPUProcess {
	var filtered []collector.GPUProcess
	for _, p := range procs {
		if p.NodeID == nodeID {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// check if ui directory exists (for dev mode)
func uiDirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
