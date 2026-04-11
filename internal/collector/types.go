package collector

// GPUDevice holds static GPU info discovered at startup.
type GPUDevice struct {
	NodeID    string `json:"node_id"`
	ID        int    `json:"id"`
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	MemTotal  uint64 `json:"mem_total"` // MiB
	DriverVer string `json:"driver_ver"`
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
	ClockGfx    int     `json:"clock_gfx"`  // MHz
	ClockMem    int     `json:"clock_mem"`   // MHz
	PCIeTx      int     `json:"pcie_tx"`     // KB/s
	PCIeRx      int     `json:"pcie_rx"`     // KB/s
	PState      int     `json:"pstate"`
	EncoderUtil float64 `json:"encoder_util"`
	DecoderUtil float64 `json:"decoder_util"`
}

// GPUProcess represents a process using the GPU.
type GPUProcess struct {
	NodeID    string `json:"node_id,omitempty"`
	Timestamp int64  `json:"ts"`
	GPUID     int    `json:"gpu_id"`
	PID       uint32 `json:"pid"`
	Name      string `json:"name"`
	GPUMem    uint64 `json:"gpu_mem"` // MiB
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
	NodeID              string  `json:"node_id,omitempty"`
	Timestamp           int64   `json:"ts"`
	ModelName           string  `json:"model_name"`
	RequestsRunning     int     `json:"requests_running"`
	RequestsWaiting     int     `json:"requests_waiting"`
	KVCacheUsage        float64 `json:"kv_cache_usage"`          // 0-1
	GenerationTokensTotal int64 `json:"generation_tokens_total"`
	PromptTokensTotal   int64   `json:"prompt_tokens_total"`
	TimeToFirstTokenAvg float64 `json:"ttft_avg"`                // seconds
	TimePerOutputTokenAvg float64 `json:"tpot_avg"`              // seconds
	TokenThroughput     float64 `json:"token_throughput"`        // tok/s
	PrefixCacheHitRate  float64 `json:"prefix_cache_hit_rate"`   // 0-1
	NumPreemptions      int64   `json:"num_preemptions"`
}

// Snapshot is a complete point-in-time reading pushed via WebSocket.
type Snapshot struct {
	Type      string       `json:"type"`
	NodeID    string       `json:"node_id,omitempty"`
	Timestamp int64        `json:"ts"`
	GPUs      []GPUMetrics `json:"gpus,omitempty"`
	Host      *HostMetrics `json:"host,omitempty"`
	Processes []GPUProcess `json:"processes,omitempty"`
	VLLM      *VLLMMetrics `json:"vllm,omitempty"`
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
