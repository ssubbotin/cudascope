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

	// Open database
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Initialize GPU collector
	gpuCol, err := collector.NewGPUCollector()
	if err != nil {
		log.Fatalf("failed to initialize GPU collector: %v", err)
	}
	defer gpuCol.Shutdown()

	// Register GPU devices
	if err := db.RegisterGPUDevices(gpuCol.Devices()); err != nil {
		log.Fatalf("failed to register GPU devices: %v", err)
	}
	log.Printf("discovered %d GPU(s)", len(gpuCol.Devices()))
	for _, d := range gpuCol.Devices() {
		log.Printf("  GPU %d: %s (%d MiB, driver %s)", d.ID, d.Name, d.MemTotal, d.DriverVer)
	}

	// Host collector
	hostname, _ := os.Hostname()
	hostCol := collector.NewHostCollector(hostname)

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
	var server *api.Server
	if cfg.DevMode {
		server = api.NewServer(db, hub, nil, true, cfg.UIDir)
	} else {
		fs, err := cudascope.UIFS()
		if err != nil {
			log.Printf("warning: embedded UI not available: %v", err)
			server = api.NewServer(db, hub, nil, false, "")
		} else {
			server = api.NewServer(db, hub, fs, false, "")
		}
	}

	go func() {
		if err := server.ListenAndServe(cfg.Port); err != nil {
			log.Printf("server error: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("CudaScope stopped")
}
