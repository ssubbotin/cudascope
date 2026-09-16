package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func writeVLLMSample(t *testing.T, db *DB, ts int64, throughput float64, running int) {
	t.Helper()
	err := db.WriteVLLMMetrics(&collector.VLLMMetrics{
		NodeID:                "local",
		Timestamp:             ts,
		ModelName:             "qwen3-coder",
		RequestsRunning:       running,
		KVCacheUsage:          0.5,
		TokenThroughput:       throughput,
		GenerationTokensTotal: int64(ts % 1000),
	})
	if err != nil {
		t.Fatalf("write vllm metrics: %v", err)
	}
}

// The raw table was neither rolled up nor pruned, so it grew without bound.
// Pruning it on its own would have traded that for losing every vLLM sample
// older than a day.
func TestVLLMHistorySurvivesPruningThroughTheRollup(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	for i := int64(0); i < 5; i++ {
		writeVLLMSample(t, db, now-600+i, 100+float64(i), 2)
	}

	db.doRetention(RetentionConfig{
		Raw: time.Minute, // anything older than a minute leaves the raw table
		M1:  30 * 24 * time.Hour,
		H1:  365 * 24 * time.Hour,
	})

	var rawRows int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM vllm_metrics_raw").Scan(&rawRows); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if rawRows != 0 {
		t.Fatalf("want the raw rows pruned, %d left", rawRows)
	}

	// A day-wide window reads the minute rollup, where the samples survive.
	history, err := db.ReadVLLMMetrics("local", now-24*3600, now)
	if err != nil {
		t.Fatalf("read vllm: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("pruning the raw table lost the whole history")
	}
	if history[0].ModelName != "qwen3-coder" {
		t.Fatalf("model name did not survive the rollup: %+v", history[0])
	}
}

// Averaging a spike away hides exactly the moment worth looking at.
func TestVLLMRollupKeepsThePeakThroughput(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()
	minute := (now - 600) / 60 * 60

	writeVLLMSample(t, db, minute+10, 10, 1)
	writeVLLMSample(t, db, minute+20, 900, 8)
	writeVLLMSample(t, db, minute+30, 20, 1)

	db.doRetention(RetentionConfig{Raw: time.Minute, M1: 30 * 24 * time.Hour, H1: 365 * 24 * time.Hour})

	history, err := db.ReadVLLMMetrics("local", now-24*3600, now)
	if err != nil {
		t.Fatalf("read vllm: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("want one minute bucket, got %d", len(history))
	}
	if history[0].TokenThroughput != 900 {
		t.Fatalf("token throughput = %v, want the peak 900", history[0].TokenThroughput)
	}
	if history[0].RequestsRunning != 8 {
		t.Fatalf("requests running = %d, want the peak 8", history[0].RequestsRunning)
	}
}

// A short window still reads full resolution.
func TestVLLMShortRangeReadsRawSamples(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	for i := int64(0); i < 4; i++ {
		writeVLLMSample(t, db, now-30+i, 100+float64(i), 2)
	}

	history, err := db.ReadVLLMMetrics("local", now-300, now)
	if err != nil {
		t.Fatalf("read vllm: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("want the four raw samples, got %d", len(history))
	}
}
