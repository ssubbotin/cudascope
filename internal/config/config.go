package config

import (
	"flag"
	"os"
	"time"
)

type Config struct {
	Mode                  string
	Port                  int
	DataDir               string
	HubURL                string
	NodeID                string
	CollectInterval       time.Duration
	HostInterval          time.Duration
	CollectStaleAfter     time.Duration
	CollectStallExitAfter time.Duration
	RetentionRaw          time.Duration
	Retention1m           time.Duration
	Retention1h           time.Duration
	DevMode               bool
	UIDir                 string
	Auth                  string        // "user:password" for basic auth (empty = disabled)
	AlertTempMax          int           // temperature alert threshold (°C, 0 = disabled)
	AlertGPUUtil          int           // GPU utilization alert threshold (%, 0 = disabled)
	AlertMemUtil          int           // memory utilization alert threshold (%, 0 = disabled)
	AlertFor              time.Duration // how long a breach must hold before an alert opens
	AlertClear            time.Duration // how long normality must hold before it closes
	NodeOfflineAfter      time.Duration // heartbeat age at which a node counts as offline
	RetentionAlerts       time.Duration // how long closed alert events are kept
	VLLMUrl               string        // vLLM metrics endpoint base URL (empty = disabled)
	VLLMInterval          time.Duration
}

func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Mode, "mode", envOrDefault("CUDASCOPE_MODE", "standalone"), "operating mode: standalone, hub, agent")
	flag.IntVar(&cfg.Port, "port", envOrDefaultInt("CUDASCOPE_PORT", 9090), "HTTP listen port")
	flag.StringVar(&cfg.DataDir, "data-dir", envOrDefault("CUDASCOPE_DATA_DIR", "/data"), "data directory for SQLite")
	flag.StringVar(&cfg.HubURL, "hub-url", envOrDefault("CUDASCOPE_HUB_URL", ""), "hub URL (agent mode)")
	flag.StringVar(&cfg.NodeID, "node-id", envOrDefault("CUDASCOPE_NODE_ID", ""), "node identifier (default: hostname)")
	flag.DurationVar(&cfg.CollectInterval, "collect-interval", envOrDefaultDuration("CUDASCOPE_COLLECT_INTERVAL", time.Second), "GPU metric collection interval")
	flag.DurationVar(&cfg.HostInterval, "host-interval", envOrDefaultDuration("CUDASCOPE_HOST_INTERVAL", 5*time.Second), "host metric collection interval")
	flag.DurationVar(&cfg.CollectStaleAfter, "collect-stale-after", envOrDefaultDuration("CUDASCOPE_COLLECT_STALE_AFTER", time.Minute), "standalone only: healthz fails when no GPU metric has been collected for this long (0 = disabled)")
	flag.DurationVar(&cfg.CollectStallExitAfter, "collect-stall-exit-after", envOrDefaultDuration("CUDASCOPE_COLLECT_STALL_EXIT_AFTER", 5*time.Minute), "standalone only: exit when no GPU metric has been collected for this long, so the supervisor restarts us (0 = disabled)")
	flag.DurationVar(&cfg.RetentionRaw, "retention-raw", envOrDefaultDuration("CUDASCOPE_RETENTION_RAW", 24*time.Hour), "raw metrics retention")
	flag.DurationVar(&cfg.Retention1m, "retention-1m", envOrDefaultDuration("CUDASCOPE_RETENTION_1M", 30*24*time.Hour), "1-minute rollup retention")
	flag.DurationVar(&cfg.Retention1h, "retention-1h", envOrDefaultDuration("CUDASCOPE_RETENTION_1H", 365*24*time.Hour), "1-hour rollup retention")
	flag.BoolVar(&cfg.DevMode, "dev", false, "development mode (serve UI from filesystem)")
	flag.StringVar(&cfg.UIDir, "ui-dir", "ui/build", "UI directory (dev mode)")
	flag.StringVar(&cfg.Auth, "auth", envOrDefault("CUDASCOPE_AUTH", ""), "basic auth credentials (user:password)")
	flag.IntVar(&cfg.AlertTempMax, "alert-temp", envOrDefaultInt("CUDASCOPE_ALERT_TEMP", 0), "temperature alert threshold °C (0=disabled)")
	flag.IntVar(&cfg.AlertGPUUtil, "alert-gpu-util", envOrDefaultInt("CUDASCOPE_ALERT_GPU_UTIL", 0), "GPU utilization alert threshold % (0=disabled)")
	flag.IntVar(&cfg.AlertMemUtil, "alert-mem-util", envOrDefaultInt("CUDASCOPE_ALERT_MEM_UTIL", 0), "memory utilization alert threshold % (0=disabled)")
	flag.DurationVar(&cfg.AlertFor, "alert-for", envOrDefaultDuration("CUDASCOPE_ALERT_FOR", 30*time.Second), "how long a threshold must be exceeded before an alert opens")
	flag.DurationVar(&cfg.AlertClear, "alert-clear", envOrDefaultDuration("CUDASCOPE_ALERT_CLEAR", time.Minute), "how long a metric must be back to normal before an alert closes")
	flag.DurationVar(&cfg.NodeOfflineAfter, "node-offline-after", envOrDefaultDuration("CUDASCOPE_NODE_OFFLINE_AFTER", time.Minute), "silence after which a node counts as offline and raises an alert")
	flag.DurationVar(&cfg.RetentionAlerts, "retention-alerts", envOrDefaultDuration("CUDASCOPE_RETENTION_ALERTS", 90*24*time.Hour), "closed alert event retention")
	flag.StringVar(&cfg.VLLMUrl, "vllm-url", envOrDefault("CUDASCOPE_VLLM_URL", ""), "vLLM metrics endpoint base URL (empty=disabled)")
	flag.DurationVar(&cfg.VLLMInterval, "vllm-interval", envOrDefaultDuration("CUDASCOPE_VLLM_INTERVAL", 5*time.Second), "vLLM metrics collection interval")

	flag.Parse()
	return cfg
}

// FreshWindow is how old the newest sample may be and still count as
// current. It follows the collection interval: a window fixed at 30 seconds
// made --collect-interval 60s produce an empty dashboard.
func (c *Config) FreshWindow() time.Duration {
	window := 5 * c.CollectInterval
	if window < 30*time.Second {
		window = 30 * time.Second
	}
	return window
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrDefaultInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var i int
	for _, c := range v {
		if c >= '0' && c <= '9' {
			i = i*10 + int(c-'0')
		}
	}
	return i
}

func envOrDefaultDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
