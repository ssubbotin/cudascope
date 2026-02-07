package collector

import (
	"context"
	"log"
	"time"
)

// MetricSink receives collected metrics.
type MetricSink interface {
	WriteGPUMetrics(metrics []GPUMetrics) error
	WriteHostMetrics(m *HostMetrics) error
	WriteGPUProcesses(procs []GPUProcess) error
}

// BroadcastSink receives snapshots for real-time push.
type BroadcastSink interface {
	Broadcast(snap Snapshot)
}

// Collector orchestrates GPU and host metric collection.
type Collector struct {
	gpu       *GPUCollector
	host      *HostCollector
	storage   MetricSink
	broadcast BroadcastSink

	gpuInterval  time.Duration
	hostInterval time.Duration
}

// New creates a new Collector.
func New(gpu *GPUCollector, host *HostCollector, storage MetricSink, broadcast BroadcastSink, gpuInterval, hostInterval time.Duration) *Collector {
	return &Collector{
		gpu:          gpu,
		host:         host,
		storage:      storage,
		broadcast:    broadcast,
		gpuInterval:  gpuInterval,
		hostInterval: hostInterval,
	}
}

// Run starts collection loops. Blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	gpuTicker := time.NewTicker(c.gpuInterval)
	hostTicker := time.NewTicker(c.hostInterval)
	defer gpuTicker.Stop()
	defer hostTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-gpuTicker.C:
			c.collectGPU()

		case <-hostTicker.C:
			c.collectHost()
		}
	}
}

func (c *Collector) collectGPU() {
	metrics := c.gpu.Collect()

	if err := c.storage.WriteGPUMetrics(metrics); err != nil {
		log.Printf("error writing GPU metrics: %v", err)
	}

	if c.broadcast != nil {
		c.broadcast.Broadcast(Snapshot{
			Type:      "gpu_metrics",
			Timestamp: time.Now().Unix(),
			GPUs:      metrics,
		})
	}

	// Collect processes alongside GPU metrics (less frequent internally)
	procs := c.gpu.CollectProcesses()
	if len(procs) > 0 {
		if err := c.storage.WriteGPUProcesses(procs); err != nil {
			log.Printf("error writing GPU processes: %v", err)
		}
		if c.broadcast != nil {
			c.broadcast.Broadcast(Snapshot{
				Type:      "gpu_processes",
				Timestamp: time.Now().Unix(),
				Processes: procs,
			})
		}
	}
}

func (c *Collector) collectHost() {
	m, err := c.host.Collect()
	if err != nil {
		log.Printf("error collecting host metrics: %v", err)
		return
	}

	if err := c.storage.WriteHostMetrics(m); err != nil {
		log.Printf("error writing host metrics: %v", err)
	}

	if c.broadcast != nil {
		c.broadcast.Broadcast(Snapshot{
			Type:      "host_metrics",
			Timestamp: time.Now().Unix(),
			Host:      m,
		})
	}
}
