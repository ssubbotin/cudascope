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

	"github.com/sergey/cudascope/internal/storage"
)

// Server is the HTTP API server.
type Server struct {
	store  *storage.DB
	hub    *Hub
	mux    *http.ServeMux
	uiFS   fs.FS    // embedded or filesystem UI
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
	s.mux.HandleFunc("/api/v1/status", s.handleStatus)
	s.mux.HandleFunc("/api/v1/gpus", s.handleGPUs)
	s.mux.HandleFunc("/api/v1/gpus/", s.handleGPURoute)
	s.mux.HandleFunc("/api/v1/host/metrics", s.handleHostMetrics)
	s.mux.HandleFunc("/api/v1/ws", s.hub.HandleWS)
	s.mux.HandleFunc("/api/v1/healthz", s.handleHealthz)

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
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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

// handleStatus returns the current snapshot of all GPUs and host.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	gpus, err := s.store.GetLatestGPUMetrics()
	if err != nil {
		httpError(w, "get gpu metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	host, err := s.store.GetLatestHostMetrics()
	if err != nil {
		httpError(w, "get host metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	devices, err := s.store.GetGPUDevices()
	if err != nil {
		httpError(w, "get devices: "+err.Error(), http.StatusInternalServerError)
		return
	}

	procs, _ := s.store.GetAllGPUProcesses()

	resp := map[string]any{
		"devices":   devices,
		"gpus":      gpus,
		"host":      host,
		"processes": procs,
	}
	writeJSON(w, resp)
}

// handleGPUs lists GPU devices.
func (s *Server) handleGPUs(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.GetGPUDevices()
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
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
	metrics, err := s.store.GetGPUMetrics(storage.GPUMetricsQuery{
		GPUID: gpuID,
		From:  from,
		To:    to,
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
	procs, err := s.store.GetGPUProcesses(gpuID)
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
	metrics, err := s.store.GetHostMetrics(from, to)
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
