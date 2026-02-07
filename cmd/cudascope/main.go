package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	cudascope "github.com/sergey/cudascope"
	"github.com/sergey/cudascope/internal/agent"
	"github.com/sergey/cudascope/internal/api"
	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/config"
	"github.com/sergey/cudascope/internal/storage"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	cfg := config.Load()

	// Healthcheck mode: just probe the HTTP endpoint and exit
	if cfg.Mode == "healthcheck" {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/healthz", cfg.Port))
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	log.Printf("CudaScope starting (mode=%s, port=%d)", cfg.Mode, cfg.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
	}()

	switch cfg.Mode {
	case "standalone":
		runStandalone(ctx, cancel, cfg)
	case "hub":
		runHub(ctx, cancel, cfg)
	case "agent":
		runAgent(ctx, cancel, cfg)
	default:
		log.Fatalf("unknown mode: %s", cfg.Mode)
	}

	<-ctx.Done()
	log.Println("CudaScope stopped")
}

func runStandalone(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) {
	// Open database
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	go func() { <-ctx.Done(); db.Close() }()

	// Register local node
	hostname, _ := os.Hostname()
	db.RegisterNode("local", hostname, 0)

	// Initialize GPU collector
	gpuCol, err := collector.NewGPUCollector()
	if err != nil {
		log.Fatalf("failed to initialize GPU collector: %v", err)
	}
	go func() { <-ctx.Done(); gpuCol.Shutdown() }()

	// Register GPU devices under 'local' node
	if err := db.RegisterGPUDevices("local", gpuCol.Devices()); err != nil {
		log.Fatalf("failed to register GPU devices: %v", err)
	}
	db.RegisterNode("local", hostname, len(gpuCol.Devices()))
	logDevices(gpuCol.Devices())

	// Host collector
	hostCol := collector.NewHostCollector("local")

	// WebSocket hub
	hub := api.NewHub()

	// Start collector
	col := collector.New(gpuCol, hostCol, db, hub, cfg.CollectInterval, cfg.HostInterval)
	go col.Run(ctx)

	// Start retention
	go db.RunRetention(ctx, storage.RetentionConfig{
		Raw: cfg.RetentionRaw,
		M1:  cfg.Retention1m,
		H1:  cfg.Retention1h,
	})

	// Start API server
	server := newAPIServer(db, hub, cfg)
	go func() {
		if err := server.ListenAndServe(cfg.Port); err != nil {
			log.Printf("server error: %v", err)
			cancel()
		}
	}()
}

func runHub(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) {
	// Open database
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	go func() { <-ctx.Done(); db.Close() }()

	log.Println("running in hub mode — waiting for agent connections")

	// WebSocket hub
	hub := api.NewHub()

	// Start retention
	go db.RunRetention(ctx, storage.RetentionConfig{
		Raw: cfg.RetentionRaw,
		M1:  cfg.Retention1m,
		H1:  cfg.Retention1h,
	})

	// Start API server (with ingest endpoints)
	server := newAPIServer(db, hub, cfg)
	go func() {
		if err := server.ListenAndServe(cfg.Port); err != nil {
			log.Printf("server error: %v", err)
			cancel()
		}
	}()
}

func runAgent(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) {
	if cfg.HubURL == "" {
		log.Fatalf("agent mode requires --hub-url")
	}

	// Determine node ID
	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID, _ = os.Hostname()
	}
	log.Printf("agent node_id=%s, hub=%s", nodeID, cfg.HubURL)

	// Initialize GPU collector
	gpuCol, err := collector.NewGPUCollector()
	if err != nil {
		log.Fatalf("failed to initialize GPU collector: %v", err)
	}
	go func() { <-ctx.Done(); gpuCol.Shutdown() }()

	logDevices(gpuCol.Devices())

	// Host collector
	hostCol := collector.NewHostCollector(nodeID)

	// Agent sink (pushes metrics to hub)
	agentSink := agent.New(cfg.HubURL, nodeID)

	// Register with hub (retries until successful)
	go func() {
		if err := agentSink.Register(ctx, gpuCol.Devices()); err != nil {
			log.Printf("registration cancelled: %v", err)
			return
		}
	}()

	// Start collector with agent sink (no broadcast — no local WS clients)
	col := collector.New(gpuCol, hostCol, agentSink, nil, cfg.CollectInterval, cfg.HostInterval)
	go col.Run(ctx)

	// Minimal health endpoint for Docker healthcheck
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		log.Printf("agent health endpoint on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("health server error: %v", err)
		}
	}()
}

func newAPIServer(db *storage.DB, hub *api.Hub, cfg *config.Config) *api.Server {
	if cfg.DevMode {
		return api.NewServer(db, hub, nil, true, cfg.UIDir)
	}
	fs, err := cudascope.UIFS()
	if err != nil {
		log.Printf("warning: embedded UI not available: %v", err)
		return api.NewServer(db, hub, nil, false, "")
	}
	return api.NewServer(db, hub, fs, false, "")
}

func logDevices(devices []collector.GPUDevice) {
	log.Printf("discovered %d GPU(s)", len(devices))
	for _, d := range devices {
		log.Printf("  GPU %d: %s (%d MiB, driver %s)", d.ID, d.Name, d.MemTotal, d.DriverVer)
	}
}
