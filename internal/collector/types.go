package collector

// GPUDevice holds static GPU info discovered at startup.
type GPUDevice struct {
	NodeID    string `json:"node_id"`
	ID        int    `json:"id"`
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	MemTotal  uint64 `json:"mem_total"` // MiB
	DriverVer string `json:"driver_ver"`

	// EccSupported and ThrottleSupported record what this card can answer,
	// asked once at startup. A card that cannot report ECC must not be drawn
	// as a card with no errors.
	EccSupported      bool `json:"ecc_supported"`
	ThrottleSupported bool `json:"throttle_supported"`
}

// GPUMetrics holds a single snapshot of GPU metrics.
type GPUMetrics struct {
	NodeID      string  `json:"node_id,omitempty"`
	Timestamp   int64   `json:"ts"`
	GPUID       int     `json:"gpu_id"`
	GPUUtil     float64 `json:"gpu_util"`
	MemUtil     float64 `json:"mem_util"`
	MemUsed     uint64  `json:"mem_used"` // MiB
	Temperature int     `json:"temperature"`
	FanSpeed    int     `json:"fan_speed"`
	PowerDraw   float64 `json:"power_draw"` // W
	PowerLimit  float64 `json:"power_limit"`
	ClockGfx    int     `json:"clock_gfx"` // MHz
	ClockMem    int     `json:"clock_mem"` // MHz
	PCIeTx      int     `json:"pcie_tx"`   // KB/s
	PCIeRx      int     `json:"pcie_rx"`   // KB/s
	PState      int     `json:"pstate"`
	EncoderUtil float64 `json:"encoder_util"`
	DecoderUtil float64 `json:"decoder_util"`

	// ThrottleReasons is the NVML clocks-throttle bitmask. It answers the
	// first question asked when a card slows down: whether it slowed itself,
	// and why.
	ThrottleReasons uint64 `json:"throttle_reasons"`

	// EccCorrected and EccUncorrected are lifetime counters. Cards that do
	// not implement ECC report neither, which is why GPUDevice carries
	// whether this card can answer at all.
	EccCorrected   uint64 `json:"ecc_corrected"`
	EccUncorrected uint64 `json:"ecc_uncorrected"`
}

// throttlingMask covers the reasons that mean the card is being held back.
// Idle and the two "someone set the clocks" reasons are states, not
// slowdowns, and reporting them as throttling would make an idle GPU look
// like a problem.
const throttlingMask = nvmlThrottleSwPowerCap |
	nvmlThrottleHwSlowdown |
	nvmlThrottleSyncBoost |
	nvmlThrottleSwThermal |
	nvmlThrottleHwThermal |
	nvmlThrottleHwPowerBrake

// Named here rather than imported so this file stays free of the NVML
// package: the values are part of the stored format and the wire format.
const (
	nvmlThrottleGpuIdle       = 1
	nvmlThrottleAppClocks     = 2
	nvmlThrottleSwPowerCap    = 4
	nvmlThrottleHwSlowdown    = 8
	nvmlThrottleSyncBoost     = 16
	nvmlThrottleSwThermal     = 32
	nvmlThrottleHwThermal     = 64
	nvmlThrottleHwPowerBrake  = 128
	nvmlThrottleDisplayClocks = 256
)

// Throttled reports whether the card is being held back right now.
func (m GPUMetrics) Throttled() bool {
	return m.ThrottleReasons&throttlingMask != 0
}

// GPUProcess represents a process using the GPU.
type GPUProcess struct {
	NodeID    string `json:"node_id,omitempty"`
	Timestamp int64  `json:"ts"`
	GPUID     int    `json:"gpu_id"`
	PID       uint32 `json:"pid"`
	Name      string `json:"name"`
	// Cmdline is the command line with secret values masked, so a list of
	// processes all called "python" says which job each one is. Empty when
	// it could not be read.
	Cmdline string `json:"cmdline,omitempty"`
	GPUMem  uint64 `json:"gpu_mem"` // MiB
}

// HostMetrics holds a snapshot of host-level metrics.
type HostMetrics struct {
	Timestamp  int64   `json:"ts"`
	NodeID     string  `json:"node_id"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsed    uint64  `json:"mem_used"`
	MemTotal   uint64  `json:"mem_total"`
	DiskUsed   uint64  `json:"disk_used"`
	DiskTotal  uint64  `json:"disk_total"`
	NetRx      uint64  `json:"net_rx"` // bytes/s
	NetTx      uint64  `json:"net_tx"` // bytes/s
	Load1m     float64 `json:"load_1m"`
	Load5m     float64 `json:"load_5m"`
	Load15m    float64 `json:"load_15m"`
}

// VLLMMetrics holds a snapshot of vLLM inference server metrics.
type VLLMMetrics struct {
	NodeID                string  `json:"node_id,omitempty"`
	Timestamp             int64   `json:"ts"`
	ModelName             string  `json:"model_name"`
	RequestsRunning       int     `json:"requests_running"`
	RequestsWaiting       int     `json:"requests_waiting"`
	KVCacheUsage          float64 `json:"kv_cache_usage"` // 0-1
	GenerationTokensTotal int64   `json:"generation_tokens_total"`
	PromptTokensTotal     int64   `json:"prompt_tokens_total"`
	// These three are ratios over what happened between two scrapes, and a
	// window where the denominator did not move has no value to report: no
	// request finished, or no prompt was prefilled. A pointer so that
	// "nothing was measured" and "the measurement was zero" stay apart. A
	// zero here used to be stored like any reading, which drew the chart
	// down to the floor for the whole of a long generation and dragged the
	// rollup averages down with it.
	TimeToFirstTokenAvg   *float64 `json:"ttft_avg"`              // seconds
	TimePerOutputTokenAvg *float64 `json:"tpot_avg"`              // seconds
	PrefixCacheHitRate    *float64 `json:"prefix_cache_hit_rate"` // 0-1

	TokenThroughput float64 `json:"token_throughput"` // tok/s
	NumPreemptions  int64   `json:"num_preemptions"`
}

// OllamaMetrics is one reading of an ollama server: which models it holds in
// memory and how much of each one is on the GPU.
//
// Ollama publishes no counters, so there are no rates here. The numbers it
// does publish are the state of the moment, and the state is what answers
// the questions that matter: which model is loaded, whether it fits in the
// card, and when the keep alive will unload it.
type OllamaMetrics struct {
	NodeID    string        `json:"node_id,omitempty"`
	Timestamp int64         `json:"ts"`
	Version   string        `json:"version,omitempty"`
	Models    []OllamaModel `json:"models"`
}

// OllamaModel is one model held in memory.
type OllamaModel struct {
	Name string `json:"name"`
	// SizeBytes is the whole model and VRAMBytes the part of it on the GPU.
	// A VRAM share below the size means the rest is in system memory, which
	// is the usual reason a model answers slower than it did yesterday.
	SizeBytes     int64 `json:"size_bytes"`
	VRAMBytes     int64 `json:"vram_bytes"`
	ContextLength int64 `json:"context_length,omitempty"`
	// ExpiresAt is when the keep alive unloads the model, in unix seconds.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// OnGPU is the share of the model that sits in GPU memory, from 0 to 1.
func (m OllamaModel) OnGPU() float64 {
	if m.SizeBytes <= 0 {
		return 0
	}
	return float64(m.VRAMBytes) / float64(m.SizeBytes)
}

// Snapshot is a complete point-in-time reading pushed via WebSocket.
type Snapshot struct {
	Type      string         `json:"type"`
	NodeID    string         `json:"node_id,omitempty"`
	Timestamp int64          `json:"ts"`
	GPUs      []GPUMetrics   `json:"gpus,omitempty"`
	Host      *HostMetrics   `json:"host,omitempty"`
	Processes []GPUProcess   `json:"processes,omitempty"`
	VLLM      *VLLMMetrics   `json:"vllm,omitempty"`
	Ollama    *OllamaMetrics `json:"ollama,omitempty"`
}

// Node represents a registered agent node.
type Node struct {
	NodeID    string `json:"node_id"`
	Hostname  string `json:"hostname"`
	GPUCount  int    `json:"gpu_count"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Online    bool   `json:"online"`
}
