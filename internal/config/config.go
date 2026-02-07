package config

import (
	"flag"
	"os"
	"time"
)

type Config struct {
	Mode            string
	Port            int
	DataDir         string
	HubURL          string
	CollectInterval time.Duration
	HostInterval    time.Duration
	RetentionRaw    time.Duration
	Retention1m     time.Duration
	Retention1h     time.Duration
	DevMode         bool
	UIDir           string
}

func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Mode, "mode", envOrDefault("CUDASCOPE_MODE", "standalone"), "operating mode: standalone, hub, agent")
	flag.IntVar(&cfg.Port, "port", envOrDefaultInt("CUDASCOPE_PORT", 9090), "HTTP listen port")
	flag.StringVar(&cfg.DataDir, "data-dir", envOrDefault("CUDASCOPE_DATA_DIR", "/data"), "data directory for SQLite")
	flag.StringVar(&cfg.HubURL, "hub-url", envOrDefault("CUDASCOPE_HUB_URL", ""), "hub URL (agent mode)")
	flag.DurationVar(&cfg.CollectInterval, "collect-interval", envOrDefaultDuration("CUDASCOPE_COLLECT_INTERVAL", time.Second), "GPU metric collection interval")
	flag.DurationVar(&cfg.HostInterval, "host-interval", envOrDefaultDuration("CUDASCOPE_HOST_INTERVAL", 5*time.Second), "host metric collection interval")
	flag.DurationVar(&cfg.RetentionRaw, "retention-raw", envOrDefaultDuration("CUDASCOPE_RETENTION_RAW", 24*time.Hour), "raw metrics retention")
	flag.DurationVar(&cfg.Retention1m, "retention-1m", envOrDefaultDuration("CUDASCOPE_RETENTION_1M", 30*24*time.Hour), "1-minute rollup retention")
	flag.DurationVar(&cfg.Retention1h, "retention-1h", envOrDefaultDuration("CUDASCOPE_RETENTION_1H", 365*24*time.Hour), "1-hour rollup retention")
	flag.BoolVar(&cfg.DevMode, "dev", false, "development mode (serve UI from filesystem)")
	flag.StringVar(&cfg.UIDir, "ui-dir", "ui/build", "UI directory (dev mode)")

	flag.Parse()
	return cfg
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
