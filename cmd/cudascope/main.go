package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cudascope "github.com/sergey/cudascope"
	"github.com/sergey/cudascope/internal/agent"
	"github.com/sergey/cudascope/internal/alerts"
	"github.com/sergey/cudascope/internal/api"
	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/config"
	"github.com/sergey/cudascope/internal/storage"
	"github.com/sergey/cudascope/internal/watchdog"
)

// localNodeID is the node that standalone mode registers itself under.
const localNodeID = "local"

// alertSweepInterval is how often silence is checked for. It is unrelated
// to the collection interval: this looks at ages, not at samples.
const alertSweepInterval = 5 * time.Second

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

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

	var httpSrv *http.Server

	switch cfg.Mode {
	case "standalone":
		httpSrv = runStandalone(ctx, cancel, cfg)
	case "hub":
		httpSrv = runHub(ctx, cancel, cfg)
	case "agent":
		httpSrv = runAgent(ctx, cancel, cfg)
	default:
		log.Fatalf("unknown mode: %s", cfg.Mode)
	}

	<-ctx.Done()

	// Graceful HTTP shutdown (5s deadline)
	if httpSrv != nil {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}

	log.Println("CudaScope stopped")
}

func runStandalone(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) *http.Server {
	// Open database
	db, err := storage.Open(cfg.DataDir, storageOptions(cfg))
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	go func() { <-ctx.Done(); db.Close() }()

	// Register local node
	hostname, _ := os.Hostname()
	db.RegisterNode(localNodeID, hostname, 0)

	// Initialize GPU collector
	gpuCol, err := collector.NewGPUCollector()
	if err != nil {
		log.Fatalf("failed to initialize GPU collector: %v", err)
	}
	go func() { <-ctx.Done(); gpuCol.Shutdown() }()

	// Register GPU devices under 'local' node
	if err := db.RegisterGPUDevices(localNodeID, gpuCol.Devices()); err != nil {
		log.Fatalf("failed to register GPU devices: %v", err)
	}
	db.RegisterNode(localNodeID, hostname, len(gpuCol.Devices()))
	logDevices(gpuCol.Devices())

	// Host collector
	hostCol := collector.NewHostCollector(localNodeID)

	// WebSocket hub
	hub := api.NewHub()

	// Alert engine: thresholds are judged where samples are collected, so an
	// unattended dashboard is no longer an unevaluated GPU.
	engine := newAlertEngine(db, cfg, localNodeID)

	// Start collector
	col := collector.New(gpuCol, hostCol, db, hub, cfg.CollectInterval, cfg.HostInterval, cfg.ProcessInterval)
	col.SetAlerts(engine)
	enableVLLM(col, cfg, localNodeID)

	go col.Run(ctx)
	watchXid(ctx, gpuCol, func(gpuID int, xid uint64) {
		engine.NoteXid(localNodeID, gpuID, xid)
	})

	// Collection that wedges cannot be unstuck from inside: an NVML call is a
	// cgo call with no timeout. Exit instead and let the restart policy work.
	go watchdog.Run(ctx, func() (int64, error) { return db.LatestGPUMetricTs(localNodeID) }, cfg.CollectStallExitAfter, func(age time.Duration) {
		log.Fatalf("no GPU metrics for %s, exiting so the supervisor restarts us", age.Truncate(time.Second))
	})

	// Start retention
	go db.RunRetention(ctx, retentionConfig(cfg))

	go runAlertSweep(ctx, engine)

	// Start API server
	server := newAPIServer(db, hub, engine, cfg)
	go server.RunStateBroadcast(ctx)
	// Hub mode leaves this off: there the data freshness reports on the
	// agents, not on this process.
	server.SetCollectorWatchdog(localNodeID, cfg.CollectStaleAfter)
	httpSrv := server.HTTPServer(cfg.Port)
	go func() {
		log.Printf("HTTP server listening on :%d", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
			cancel()
		}
	}()
	return httpSrv
}

func runHub(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) *http.Server {
	// Open database
	db, err := storage.Open(cfg.DataDir, storageOptions(cfg))
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	go func() { <-ctx.Done(); db.Close() }()

	log.Println("running in hub mode — waiting for agent connections")
	if cfg.IngestToken == "" && cfg.Auth == "" {
		log.Println("warning: ingest is open, anything that can reach this port may register a node and store metrics (set --ingest-token)")
	}

	// WebSocket hub
	hub := api.NewHub()

	// Start retention
	go db.RunRetention(ctx, retentionConfig(cfg))

	// Hub mode judges the samples its agents push. It names no local node:
	// there is no collector of its own to call stalled.
	engine := newAlertEngine(db, cfg, "")
	go runAlertSweep(ctx, engine)

	// Start API server (with ingest endpoints)
	server := newAPIServer(db, hub, engine, cfg)
	go server.RunStateBroadcast(ctx)
	httpSrv := server.HTTPServer(cfg.Port)
	go func() {
		log.Printf("HTTP server listening on :%d", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
			cancel()
		}
	}()
	return httpSrv
}

func runAgent(ctx context.Context, cancel context.CancelFunc, cfg *config.Config) *http.Server {
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
	agentSink := agent.New(cfg.HubURL, nodeID, agent.Options{
		Token: cfg.IngestToken,
		Auth:  cfg.Auth,
	})

	// Register with hub (retries until successful)
	go func() {
		if err := agentSink.Register(ctx, gpuCol.Devices()); err != nil {
			log.Printf("registration cancelled: %v", err)
			return
		}
	}()

	// Start collector with agent sink (no broadcast — no local WS clients)
	col := collector.New(gpuCol, hostCol, agentSink, nil, cfg.CollectInterval, cfg.HostInterval, cfg.ProcessInterval)
	enableVLLM(col, cfg, nodeID)
	go col.Run(ctx)

	// The hub holds the journal, so an agent forwards what the driver tells
	// it rather than judging it here.
	watchXid(ctx, gpuCol, func(gpuID int, xid uint64) {
		if err := agentSink.WriteXid(gpuID, xid); err != nil {
			log.Printf("could not report Xid %d on GPU %d: %v", xid, gpuID, err)
		}
	})

	// Minimal health endpoint for Docker healthcheck
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// The queue depth is the one number that says whether this agent is
		// reaching its hub.
		fmt.Fprintf(w, "ok, %d payload(s) queued for the hub\n", agentSink.Pending())
	})
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}
	go func() {
		log.Printf("agent health endpoint on :%d", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health server error: %v", err)
		}
	}()
	return httpSrv
}

func newAPIServer(db *storage.DB, hub *api.Hub, engine *alerts.Engine, cfg *config.Config) *api.Server {
	opts := api.Options{
		Store:       db,
		Hub:         hub,
		Alerts:      engine,
		Auth:        cfg.Auth,
		IngestToken: cfg.IngestToken,
		CORSOrigin:  cfg.CORSOrigin,
	}

	if cfg.DevMode {
		opts.DevMode = true
		opts.UIDir = cfg.UIDir
		return api.NewServer(opts)
	}

	fs, err := cudascope.UIFS()
	if err != nil {
		log.Printf("warning: embedded UI not available: %v", err)
		return api.NewServer(opts)
	}
	opts.UIFS = fs
	return api.NewServer(opts)
}

// enableVLLM turns on vLLM scraping when a URL is configured.
//
// Shared by standalone and agent mode on purpose: the ingest route, the sink
// method and the column all existed while agent mode alone never called
// this, so a Swarm deployment had no vLLM metrics and nothing said why.
func enableVLLM(col *collector.Collector, cfg *config.Config, nodeID string) {
	if cfg.VLLMUrl == "" {
		return
	}
	col.SetVLLM(collector.NewVLLMCollector(cfg.VLLMUrl, nodeID), cfg.VLLMInterval)
	log.Printf("vLLM metrics collection enabled: %s (interval=%s)", cfg.VLLMUrl, cfg.VLLMInterval)
}

// watchXid starts the Xid event loop when the driver allows it. A driver
// that refuses registration is worth one line in the log and nothing more:
// every other metric still works.
func watchXid(ctx context.Context, gpuCol *collector.GPUCollector, onXid func(gpuID int, xid uint64)) {
	watcher, err := gpuCol.WatchXid()
	if err != nil {
		log.Printf("Xid errors are not being watched: %v", err)
		return
	}
	go watcher.Run(ctx, onXid)
}

// storageOptions ties the queries that answer "what is current" to how
// often this deployment actually collects.
func storageOptions(cfg *config.Config) storage.Options {
	return storage.Options{
		FreshWindow:      cfg.FreshWindow(),
		NodeOfflineAfter: cfg.NodeOfflineAfter,
		RawRetention:     cfg.RetentionRaw,
		M1Retention:      cfg.Retention1m,
		MaxPoints:        cfg.MaxPoints,
	}
}

func retentionConfig(cfg *config.Config) storage.RetentionConfig {
	return storage.RetentionConfig{
		Raw:    cfg.RetentionRaw,
		M1:     cfg.Retention1m,
		H1:     cfg.Retention1h,
		Alerts: cfg.RetentionAlerts,
	}
}

// newAlertEngine builds the evaluator and adopts whatever the previous run
// left open.
func newAlertEngine(db *storage.DB, cfg *config.Config, localNode string) *alerts.Engine {
	engine := alerts.New(alerts.Config{
		TempMax:           cfg.AlertTempMax,
		GPUUtil:           cfg.AlertGPUUtil,
		MemUtil:           cfg.AlertMemUtil,
		Throttle:          cfg.AlertThrottle,
		For:               cfg.AlertFor,
		Clear:             cfg.AlertClear,
		NodeOfflineAfter:  cfg.NodeOfflineAfter,
		CollectStaleAfter: cfg.CollectStaleAfter,
		LocalNodeID:       localNode,
	}, db, time.Now)

	if err := engine.Restore(); err != nil {
		log.Printf("warning: could not adopt open alert events: %v", err)
	}
	return engine
}

// runAlertSweep raises the alerts no incoming sample can raise: a node that
// stopped reporting, and collection that stopped producing.
func runAlertSweep(ctx context.Context, engine *alerts.Engine) {
	ticker := time.NewTicker(alertSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			engine.Sweep()
		}
	}
}

func logDevices(devices []collector.GPUDevice) {
	log.Printf("discovered %d GPU(s)", len(devices))
	for _, d := range devices {
		log.Printf("  GPU %d: %s (%d MiB, driver %s)", d.ID, d.Name, d.MemTotal, d.DriverVer)
	}
}
