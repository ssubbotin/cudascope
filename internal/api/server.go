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

	"github.com/sergey/cudascope/internal/alerts"
	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/storage"
)

// Options configures the API server. It is a struct because the
// constructor had grown to seven positional arguments, three of them empty
// strings at most call sites.
type Options struct {
	Store  *storage.DB
	Hub    *Hub
	Alerts *alerts.Engine // nil in agent mode: no thresholds are judged here

	UIFS    fs.FS  // embedded UI, nil when it was not built in
	DevMode bool   // serve the UI from the filesystem
	UIDir   string // where, in dev mode
	Auth    string // "user:password", empty disables

	// StateInterval is how often the state snapshot is resent even when
	// nothing changed. Zero selects the default.
	StateInterval time.Duration
}

// Server is the HTTP API server.
type Server struct {
	store   *storage.DB
	hub     *Hub
	alerts  *alerts.Engine
	mux     *http.ServeMux
	uiFS    fs.FS // embedded or filesystem UI
	devMode bool
	uiDir   string

	authUser string // basic auth (empty = disabled)
	authPass string

	// collectStaleAfter makes healthz fail when metrics stop arriving.
	// Zero disables the check.
	collectStaleAfter time.Duration
	collectNodeID     string

	stateInterval time.Duration
	stateTrigger  chan struct{}
}

// NewServer creates a new API server.
func NewServer(opts Options) *Server {
	s := &Server{
		store:         opts.Store,
		hub:           opts.Hub,
		alerts:        opts.Alerts,
		mux:           http.NewServeMux(),
		uiFS:          opts.UIFS,
		devMode:       opts.DevMode,
		uiDir:         opts.UIDir,
		stateInterval: opts.StateInterval,
		stateTrigger:  make(chan struct{}, 1),
	}
	if s.stateInterval <= 0 {
		s.stateInterval = defaultStateInterval
	}

	if opts.Auth != "" {
		parts := strings.SplitN(opts.Auth, ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			// Silence here once left a deployment wide open: a value with no
			// colon disabled authentication and said nothing about it.
			log.Printf("warning: auth credentials must read user:password, authentication stays off")
		} else {
			s.authUser, s.authPass = parts[0], parts[1]
			log.Printf("basic auth enabled for user %q", s.authUser)
		}
	}

	if s.alerts != nil {
		// Subscribed here rather than inside RunStateBroadcast so a change
		// that lands before that goroutine starts still reaches it.
		s.alerts.OnChange(s.notifyState)
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
	s.mux.HandleFunc("/api/v1/vllm/metrics", s.handleVLLMMetrics)
	s.mux.HandleFunc("/api/v1/vllm/status", s.handleVLLMStatus)
	s.mux.HandleFunc("/api/v1/alerts", s.handleAlerts)
	s.mux.HandleFunc("/api/v1/alerts/history", s.handleAlertHistory)
	s.mux.HandleFunc("/api/v1/ws", s.hub.HandleWS)
	s.mux.HandleFunc("/api/v1/healthz", s.handleHealthz)
	s.mux.HandleFunc("/metrics", s.handlePrometheus)

	// Ingest endpoints (for agent -> hub communication)
	s.mux.HandleFunc("/api/v1/ingest/register", s.handleIngestRegister)
	s.mux.HandleFunc("/api/v1/ingest/gpu-metrics", s.handleIngestGPUMetrics)
	s.mux.HandleFunc("/api/v1/ingest/host-metrics", s.handleIngestHostMetrics)
	s.mux.HandleFunc("/api/v1/ingest/gpu-processes", s.handleIngestGPUProcesses)
	s.mux.HandleFunc("/api/v1/ingest/vllm-metrics", s.handleIngestVLLMMetrics)

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

// HTTPServer creates the configured *http.Server (caller starts and shuts it down).
func (s *Server) HTTPServer(port int) *http.Server {
	addr := fmt.Sprintf(":%d", port)
	return &http.Server{
		Addr:         addr,
		Handler:      s.middleware(s.mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Basic auth (skip healthz and ingest endpoints)
		if s.authUser != "" {
			path := r.URL.Path
			if path != "/api/v1/healthz" && !strings.HasPrefix(path, "/api/v1/ingest/") {
				user, pass, ok := r.BasicAuth()
				if !ok || user != s.authUser || pass != s.authPass {
					w.Header().Set("WWW-Authenticate", `Basic realm="CudaScope"`)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.collectStaleAfter > 0 {
		ts, err := s.store.LatestGPUMetricTs(s.collectNodeID)
		if err != nil {
			httpError(w, "healthz: read latest metric: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if ts == 0 {
			httpError(w, "healthz: no GPU metrics collected yet", http.StatusServiceUnavailable)
			return
		}
		if age := time.Since(time.Unix(ts, 0)); age > s.collectStaleAfter {
			httpError(w, fmt.Sprintf("healthz: no GPU metrics for %s", age.Truncate(time.Second)), http.StatusServiceUnavailable)
			return
		}
	}

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
		"alerts":    s.openAlerts(),
	}

	// Include latest vLLM metrics if available
	vllm, err := s.store.ReadLatestVLLMMetrics(nodeFilter)
	if err != nil {
		log.Printf("get vllm metrics: %v", err)
	}
	if vllm != nil {
		resp["vllm"] = vllm
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

// --- vLLM endpoints ---

func (s *Server) handleVLLMMetrics(w http.ResponseWriter, r *http.Request) {
	from, to := parseTimeRange(r)
	nodeID := r.URL.Query().Get("node")

	metrics, err := s.store.ReadVLLMMetrics(nodeID, from, to)
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

func (s *Server) handleVLLMStatus(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")

	m, err := s.store.ReadLatestVLLMMetrics(nodeID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if m == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, m)
}

// --- Ingest endpoints (agent -> hub) ---

func (s *Server) handleIngestRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		NodeID   string                `json:"node_id"`
		Hostname string                `json:"hostname"`
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

	// Update node last_seen, check alerts, and broadcast to WebSocket clients
	if len(metrics) > 0 {
		nodeID := metrics[0].NodeID
		s.store.UpdateNodeSeen(nodeID)
		if s.alerts != nil {
			s.alerts.Observe(metrics)
		}

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

func (s *Server) handleIngestVLLMMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var m collector.VLLMMetrics
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		httpError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.store.WriteVLLMMetrics(&m); err != nil {
		httpError(w, "write vllm metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.store.UpdateNodeSeen(m.NodeID)

	s.hub.Broadcast(collector.Snapshot{
		Type:      "vllm_metrics",
		NodeID:    m.NodeID,
		Timestamp: time.Now().Unix(),
		VLLM:      &m,
	})

	w.WriteHeader(http.StatusOK)
}

// --- Prometheus ---

func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	gpus, _ := s.store.GetLatestGPUMetrics()
	devices, _ := s.store.GetGPUDevices("")
	hosts, _ := s.store.GetLatestHostMetrics()

	// Build device name lookup
	nameMap := make(map[string]string)
	for _, d := range devices {
		key := fmt.Sprintf("%s:%d", d.NodeID, d.ID)
		nameMap[key] = d.Name
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	for _, g := range gpus {
		node := g.NodeID
		if node == "" {
			node = "local"
		}
		id := strconv.Itoa(g.GPUID)
		name := nameMap[fmt.Sprintf("%s:%d", node, g.GPUID)]
		labels := fmt.Sprintf(`node_id="%s",gpu_id="%s",gpu_name="%s"`, node, id, name)

		fmt.Fprintf(w, "cudascope_gpu_utilization_percent{%s} %.1f\n", labels, g.GPUUtil)
		fmt.Fprintf(w, "cudascope_gpu_memory_used_mib{%s} %d\n", labels, g.MemUsed)
		fmt.Fprintf(w, "cudascope_gpu_memory_util_percent{%s} %.1f\n", labels, g.MemUtil)
		fmt.Fprintf(w, "cudascope_gpu_temperature_celsius{%s} %d\n", labels, g.Temperature)
		fmt.Fprintf(w, "cudascope_gpu_fan_speed_percent{%s} %d\n", labels, g.FanSpeed)
		fmt.Fprintf(w, "cudascope_gpu_power_draw_watts{%s} %.1f\n", labels, g.PowerDraw)
		fmt.Fprintf(w, "cudascope_gpu_power_limit_watts{%s} %.1f\n", labels, g.PowerLimit)
		fmt.Fprintf(w, "cudascope_gpu_clock_graphics_mhz{%s} %d\n", labels, g.ClockGfx)
		fmt.Fprintf(w, "cudascope_gpu_clock_memory_mhz{%s} %d\n", labels, g.ClockMem)
		fmt.Fprintf(w, "cudascope_gpu_pcie_tx_kbps{%s} %d\n", labels, g.PCIeTx)
		fmt.Fprintf(w, "cudascope_gpu_pcie_rx_kbps{%s} %d\n", labels, g.PCIeRx)
		fmt.Fprintf(w, "cudascope_gpu_pstate{%s} %d\n", labels, g.PState)
		fmt.Fprintf(w, "cudascope_gpu_encoder_util_percent{%s} %.1f\n", labels, g.EncoderUtil)
		fmt.Fprintf(w, "cudascope_gpu_decoder_util_percent{%s} %.1f\n", labels, g.DecoderUtil)
	}

	for _, h := range hosts {
		node := h.NodeID
		if node == "" {
			node = "local"
		}
		labels := fmt.Sprintf(`node_id="%s"`, node)
		fmt.Fprintf(w, "cudascope_host_cpu_percent{%s} %.1f\n", labels, h.CPUPercent)
		fmt.Fprintf(w, "cudascope_host_memory_used_bytes{%s} %d\n", labels, h.MemUsed)
		fmt.Fprintf(w, "cudascope_host_memory_total_bytes{%s} %d\n", labels, h.MemTotal)
		fmt.Fprintf(w, "cudascope_host_load_1m{%s} %.2f\n", labels, h.Load1m)
		fmt.Fprintf(w, "cudascope_host_load_5m{%s} %.2f\n", labels, h.Load5m)
		fmt.Fprintf(w, "cudascope_host_load_15m{%s} %.2f\n", labels, h.Load15m)
	}
}

// --- Alerts ---

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	cfg := alerts.Config{}
	if s.alerts != nil {
		cfg = s.alerts.Config()
	}

	writeJSON(w, map[string]any{
		"config": map[string]int{
			"temp_max": cfg.TempMax,
			"gpu_util": cfg.GPUUtil,
			"mem_util": cfg.MemUtil,
		},
		"alerts": s.openAlerts(),
	})
}

// handleAlertHistory serves the journal, newest first.
func (s *Server) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	from, to := parseTimeRange(r)

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	events, err := s.store.ListAlertEvents(storage.AlertEventQuery{
		From:   from,
		To:     to,
		NodeID: r.URL.Query().Get("node"),
		Kind:   r.URL.Query().Get("kind"),
		Limit:  limit,
	})
	if err != nil {
		httpError(w, "list alert events: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []alerts.Event{}
	}
	writeJSON(w, events)
}

// openAlerts is what every caller means by "the alerts": the events open
// right now, never nil so the JSON carries an empty array.
func (s *Server) openAlerts() []alerts.Event {
	if s.alerts == nil {
		return []alerts.Event{}
	}
	open := s.alerts.Active()
	if open == nil {
		return []alerts.Event{}
	}
	return open
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

// SetCollectorWatchdog makes /api/v1/healthz fail once the newest GPU metric
// row for nodeID is older than staleAfter. Zero disables the check.
//
// The node must be named: standalone mode serves the ingest endpoints too,
// so an agent pushing to this host would otherwise keep the check green
// while the local collector is wedged.
//
// Without it healthz answers "ok" as long as the HTTP goroutine is alive,
// which says nothing about collection: on 2026-09-16 the container reported
// healthy for the whole hour its collector was stuck. Call before serving.
func (s *Server) SetCollectorWatchdog(nodeID string, staleAfter time.Duration) {
	s.collectNodeID = nodeID
	s.collectStaleAfter = staleAfter
}
