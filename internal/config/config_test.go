package config

import (
	"strings"
	"testing"
	"time"
)

func base() *Config {
	return &Config{
		Mode:            "standalone",
		Port:            9090,
		CollectInterval: time.Second,
		HostInterval:    5 * time.Second,
		ProcessInterval: 5 * time.Second,
		VLLMInterval:    5 * time.Second,
		OllamaInterval:  10 * time.Second,
	}
}

// Credentials without a colon used to disable authentication in silence: the
// server started, answered every request, and said nothing about the
// protection that was asked for and never applied.
func TestAuthWithoutAColonIsRefused(t *testing.T) {
	cfg := base()
	cfg.Auth = "secret"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("credentials with no colon were accepted")
	}
	if !strings.Contains(err.Error(), "user:password") {
		t.Fatalf("the error does not say what the value should look like: %v", err)
	}
}

func TestAuthWithAnEmptyUserIsRefused(t *testing.T) {
	cfg := base()
	cfg.Auth = ":secret"

	if err := cfg.Validate(); err == nil {
		t.Fatal("credentials with an empty user were accepted")
	}
}

func TestAuthWithAColonIsAccepted(t *testing.T) {
	cfg := base()
	cfg.Auth = "admin:hunter2"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid credentials refused: %v", err)
	}
}

// Raw rows are keyed by whole seconds, so two samples inside one second are
// one sample as far as storage is concerned. Saying so beats storing half of
// what was collected.
func TestASubSecondCollectIntervalIsRefused(t *testing.T) {
	cfg := base()
	cfg.CollectInterval = 500 * time.Millisecond

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a sub-second collection interval was accepted")
	}
	if !strings.Contains(err.Error(), "collect-interval") {
		t.Fatalf("the error does not name the offending flag: %v", err)
	}
}

func TestEverySamplingIntervalIsChecked(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Config)
		flag string
	}{
		{"host", func(c *Config) { c.HostInterval = 100 * time.Millisecond }, "host-interval"},
		{"process", func(c *Config) { c.ProcessInterval = 0 }, "process-interval"},
		{"vllm", func(c *Config) { c.VLLMInterval = -time.Second }, "vllm-interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.set(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("%s interval below a second was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Fatalf("the error does not name %s: %v", tc.flag, err)
			}
		})
	}
}

func TestAgentModeNeedsAHub(t *testing.T) {
	cfg := base()
	cfg.Mode = "agent"

	if err := cfg.Validate(); err == nil {
		t.Fatal("agent mode without a hub URL was accepted")
	}

	cfg.HubURL = "http://hub:9090"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("agent mode with a hub URL refused: %v", err)
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	cfg := base()
	cfg.Mode = "cluster"

	if err := cfg.Validate(); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// The freshness window follows the collection interval, so a slower
// collector does not leave the dashboard looking empty.
func TestFreshWindowFollowsTheCollectionInterval(t *testing.T) {
	cfg := base()
	cfg.CollectInterval = time.Minute

	if got := cfg.FreshWindow(); got != 5*time.Minute {
		t.Fatalf("fresh window = %s, want 5m", got)
	}

	cfg.CollectInterval = time.Second
	if got := cfg.FreshWindow(); got != 30*time.Second {
		t.Fatalf("fresh window = %s, want the 30s floor", got)
	}
}
