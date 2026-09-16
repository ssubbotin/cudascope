package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func seedRollupMinutes(t *testing.T, db *DB, start int64, count int, utilMax float64) {
	t.Helper()
	for i := 0; i < count; i++ {
		_, err := db.conn.Exec(
			`INSERT OR REPLACE INTO gpu_metrics_1m
				(ts, node_id, gpu_id, gpu_util_avg, gpu_util_max, mem_util_avg, mem_used_avg, mem_used_max,
				 temperature_avg, temperature_max, fan_speed_avg, power_draw_avg, power_draw_max,
				 clock_gfx_avg, clock_mem_avg, pcie_tx_avg, pcie_rx_avg)
			 VALUES (?, 'local', 0, 50, ?, 40, 1024, 2048, 60, 70, 30, 200, 300, 1500, 9000, 10, 20)`,
			start+int64(i)*60, utilMax)
		if err != nil {
			t.Fatalf("seed rollup: %v", err)
		}
	}
}

// Raw rows are pruned after a day, so a half-hour window three days back has
// no raw data. Picking the table by width alone drew an empty chart for
// every narrow window in the past.
func TestANarrowWindowInThePastReadsTheRollup(t *testing.T) {
	db := openTestDBWith(t, Options{})
	old := time.Now().Add(-72 * time.Hour).Unix()
	seedRollupMinutes(t, db, old, 30, 90)

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{GPUID: 0, NodeID: "local", From: old, To: old + 1800})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("a narrow window older than raw retention came back empty")
	}
	if metrics[0].GPUUtil != 90 {
		t.Fatalf("want the rollup peak, got %v", metrics[0].GPUUtil)
	}
}

func TestARecentNarrowWindowStillReadsRaw(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	for i := int64(0); i < 20; i++ {
		writeGPUSample(t, db, 0, now-60+i, 71)
	}

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{GPUID: 0, NodeID: "local", From: now - 300, To: now})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) != 20 {
		t.Fatalf("want the 20 raw samples, got %d", len(metrics))
	}
	if metrics[0].Temperature != 71 {
		t.Fatalf("raw value lost: %+v", metrics[0])
	}
}

// A month-wide chart used to arrive as tens of thousands of points, every
// one of them drawn onto the same thousand pixels.
func TestAWideRangeIsCappedAtTheRequestedPoints(t *testing.T) {
	db := openTestDBWith(t, Options{MaxPoints: 100})
	start := time.Now().Add(-20 * 24 * time.Hour).Unix()
	seedRollupMinutes(t, db, start, 600, 80) // ten hours of minute rows

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{
		GPUID: 0, NodeID: "local", From: start, To: start + 600*60,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) > 100 {
		t.Fatalf("want at most 100 points, got %d", len(metrics))
	}
	if len(metrics) < 50 {
		t.Fatalf("the cap threw away too much: %d points", len(metrics))
	}
}

// Folding points together must not average the spike away: the peak is the
// reason anybody opens a month-wide chart.
func TestFoldingKeepsThePeak(t *testing.T) {
	db := openTestDBWith(t, Options{MaxPoints: 10})
	start := time.Now().Add(-20 * 24 * time.Hour).Unix()

	seedRollupMinutes(t, db, start, 120, 30)
	// One minute inside the window peaked at 99.
	if _, err := db.conn.Exec(
		`UPDATE gpu_metrics_1m SET gpu_util_max = 99 WHERE ts = ?`, start+60*60); err != nil {
		t.Fatalf("spike: %v", err)
	}

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{
		GPUID: 0, NodeID: "local", From: start, To: start + 120*60,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	peak := 0.0
	for _, m := range metrics {
		if m.GPUUtil > peak {
			peak = m.GPUUtil
		}
	}
	if peak != 99 {
		t.Fatalf("the spike did not survive folding: peak %v in %d points", peak, len(metrics))
	}
}

func TestHostHistoryIsCappedToo(t *testing.T) {
	db := openTestDBWith(t, Options{MaxPoints: 50})
	now := time.Now().Unix()

	for i := int64(0); i < 900; i++ {
		err := db.WriteHostMetrics(&collector.HostMetrics{
			NodeID: "local", Timestamp: now - 900 + i, CPUPercent: float64(i % 100), MemUsed: 1024,
		})
		if err != nil {
			t.Fatalf("write host: %v", err)
		}
	}

	metrics, err := db.GetHostMetrics(now-900, now, "local")
	if err != nil {
		t.Fatalf("read host: %v", err)
	}
	if len(metrics) > 50 {
		t.Fatalf("want at most 50 host points, got %d", len(metrics))
	}
}

func TestVLLMHistoryIsCappedToo(t *testing.T) {
	db := openTestDBWith(t, Options{MaxPoints: 50})
	now := time.Now().Unix()

	for i := int64(0); i < 600; i++ {
		writeVLLMSample(t, db, now-600+i, float64(i), 2)
	}

	metrics, err := db.ReadVLLMMetrics("local", now-600, now)
	if err != nil {
		t.Fatalf("read vllm: %v", err)
	}
	if len(metrics) > 50 {
		t.Fatalf("want at most 50 vLLM points, got %d", len(metrics))
	}
}

func TestZeroMaxPointsMeansNoCap(t *testing.T) {
	db := openTestDBWith(t, Options{MaxPoints: 0})
	now := time.Now().Unix()

	for i := int64(0); i < 120; i++ {
		writeGPUSample(t, db, 0, now-120+i, 60)
	}

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{GPUID: 0, NodeID: "local", From: now - 300, To: now})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) != 120 {
		t.Fatalf("an unset cap changed the answer: %d points", len(metrics))
	}
}
