package collector

import (
	"context"
	"log"
	"sync"
	"time"
)

// MetricSink receives collected metrics.
type MetricSink interface {
	WriteGPUMetrics(metrics []GPUMetrics) error
	WriteHostMetrics(m *HostMetrics) error
	WriteGPUProcesses(procs []GPUProcess) error
	WriteVLLMMetrics(m *VLLMMetrics) error
	WriteOllamaMetrics(m *OllamaMetrics) error
}

// BroadcastSink receives snapshots for real-time push.
type BroadcastSink interface {
	Broadcast(snap Snapshot)
}

// AlertObserver evaluates samples against thresholds. It lives on the
// collection path so that thresholds are checked on every reading, whether
// or not anybody has the dashboard open.
type AlertObserver interface {
	Observe(metrics []GPUMetrics)
}

// gpuSource, hostSource and vllmSource are the metric sources the collector
// drives. They exist so the loop can be tested without a GPU.
type gpuSource interface {
	Collect() []GPUMetrics
	CollectProcesses() []GPUProcess
}

type hostSource interface {
	Collect() (*HostMetrics, error)
}

type vllmSource interface {
	Collect() (*VLLMMetrics, error)
}

type ollamaSource interface {
	Collect() (*OllamaMetrics, error)
}

// defaultProcInterval is how often the process list is enumerated when
// nothing else is configured. It is deliberately slower than the metric
// tick: the list changes when a job starts or ends, while enumerating it
// costs a /proc read per process and a stored row per process.
const defaultProcInterval = 5 * time.Second

// defaultGPUInterval, defaultHostInterval and defaultVLLMInterval back up a
// source whose interval was never set.
const (
	defaultGPUInterval  = time.Second
	defaultHostInterval = 5 * time.Second
	defaultVLLMInterval = 5 * time.Second
	// Ollama publishes state rather than counters, and the state changes
	// when a model is loaded or unloaded. Ten seconds is dense enough to
	// catch that and idle enough to cost nothing.
	defaultOllamaInterval = 10 * time.Second
)

// Collector orchestrates GPU and host metric collection.
type Collector struct {
	gpu       gpuSource
	host      hostSource
	vllm      vllmSource
	ollama    ollamaSource
	storage   MetricSink
	broadcast BroadcastSink
	alerts    AlertObserver

	gpuInterval    time.Duration
	hostInterval   time.Duration
	procInterval   time.Duration
	vllmInterval   time.Duration
	ollamaInterval time.Duration

	// gpuSilent tracks whether the GPUs have stopped answering, so the
	// transition is logged once instead of every tick.
	gpuSilent bool
}

// New creates a new Collector. A zero procInterval falls back to
// defaultProcInterval.
func New(gpu *GPUCollector, host *HostCollector, storage MetricSink, broadcast BroadcastSink, gpuInterval, hostInterval, procInterval time.Duration) *Collector {
	if procInterval <= 0 {
		procInterval = defaultProcInterval
	}
	return &Collector{
		gpu:          gpu,
		host:         host,
		storage:      storage,
		broadcast:    broadcast,
		gpuInterval:  gpuInterval,
		hostInterval: hostInterval,
		procInterval: procInterval,
	}
}

// SetAlerts configures threshold evaluation. Agent mode leaves it unset:
// agents push raw samples and the hub, which holds the thresholds, judges
// them.
func (c *Collector) SetAlerts(observer AlertObserver) {
	if observer == nil {
		return
	}
	c.alerts = observer
}

// SetVLLM configures vLLM metrics collection.
func (c *Collector) SetVLLM(vllm vllmSource, interval time.Duration) {
	if vllm == nil {
		return
	}
	c.vllm = vllm
	c.vllmInterval = interval
}

// SetOllama configures ollama metrics collection.
func (c *Collector) SetOllama(ollama ollamaSource, interval time.Duration) {
	if ollama == nil {
		return
	}
	c.ollama = ollama
	c.ollamaInterval = interval
}

// Run starts one collection loop per metric source. Blocks until ctx is
// cancelled.
//
// Every source gets its own goroutine on purpose. A single select loop
// driving all three means one blocking call stops every metric at once, and
// nothing about that is visible from outside: NVML calls are cgo calls with
// no timeout, and a wedged driver or a stalled websocket write silently ends
// collection while the process keeps serving HTTP.
func (c *Collector) Run(ctx context.Context) {
	var wg sync.WaitGroup

	start := func(interval time.Duration, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runTicker(ctx, interval, fn)
		}()
	}

	start(atLeast(c.gpuInterval, defaultGPUInterval), c.collectGPU)
	start(atLeast(c.hostInterval, defaultHostInterval), c.collectHost)
	start(atLeast(c.procInterval, defaultProcInterval), c.collectProcesses)

	if c.vllm != nil {
		start(atLeast(c.vllmInterval, defaultVLLMInterval), c.collectVLLM)
	}

	if c.ollama != nil {
		start(atLeast(c.ollamaInterval, defaultOllamaInterval), c.collectOllama)
	}

	wg.Wait()
}

// atLeast keeps a ticker from being built with a zero interval, which
// panics and takes the whole process down. An unset interval is a
// configuration gap, and falling back to a default beats dying.
func atLeast(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// runTicker calls fn on every tick until ctx is done. A slow fn only costs
// its own ticks, because time.Ticker drops them rather than queueing.
func runTicker(ctx context.Context, interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}

func (c *Collector) collectGPU() {
	metrics := c.gpu.Collect()

	// Nothing answered. Storing an empty batch would refresh the freshness
	// checks without a single measurement behind them.
	if len(metrics) == 0 {
		if !c.gpuSilent {
			c.gpuSilent = true
			log.Printf("no GPU answered; samples are not being stored")
		}
		return
	}
	if c.gpuSilent {
		c.gpuSilent = false
		log.Printf("GPUs are answering again")
	}

	if err := c.storage.WriteGPUMetrics(metrics); err != nil {
		log.Printf("error writing GPU metrics: %v", err)
	}

	if c.alerts != nil {
		c.alerts.Observe(metrics)
	}

	if c.broadcast != nil {
		c.broadcast.Broadcast(Snapshot{
			Type:      "gpu_metrics",
			Timestamp: time.Now().Unix(),
			GPUs:      metrics,
		})
	}
}

// collectProcesses runs on its own loop, at its own interval.
func (c *Collector) collectProcesses() {
	procs := c.gpu.CollectProcesses()
	if len(procs) == 0 {
		return
	}

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

func (c *Collector) collectOllama() {
	m, err := c.ollama.Collect()
	if err != nil {
		log.Printf("error collecting ollama metrics: %v", err)
		return
	}

	if err := c.storage.WriteOllamaMetrics(m); err != nil {
		log.Printf("error writing ollama metrics: %v", err)
	}

	if c.broadcast != nil {
		c.broadcast.Broadcast(Snapshot{
			Type:      "ollama_metrics",
			Timestamp: time.Now().Unix(),
			Ollama:    m,
		})
	}
}

func (c *Collector) collectVLLM() {
	m, err := c.vllm.Collect()
	if err != nil {
		log.Printf("error collecting vLLM metrics: %v", err)
		return
	}

	if err := c.storage.WriteVLLMMetrics(m); err != nil {
		log.Printf("error writing vLLM metrics: %v", err)
	}

	if c.broadcast != nil {
		c.broadcast.Broadcast(Snapshot{
			Type:      "vllm_metrics",
			Timestamp: time.Now().Unix(),
			VLLM:      m,
		})
	}
}
