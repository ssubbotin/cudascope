package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// An agent that buffers and resends cannot know whether a failed POST was
// stored before the connection broke, so the same batch can arrive twice.
// Storing it twice would double every rollup it lands in.
func TestAResentGPUBatchIsStoredOnce(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()

	batch := []collector.GPUMetrics{
		{NodeID: "gpu-node-1", GPUID: 0, Timestamp: ts, Temperature: 61, GPUUtil: 50},
		{NodeID: "gpu-node-1", GPUID: 1, Timestamp: ts, Temperature: 62, GPUUtil: 60},
	}
	for i := 0; i < 3; i++ {
		if err := db.WriteGPUMetrics(batch); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	if got := countRows(t, db, "gpu_metrics_raw"); got != 2 {
		t.Fatalf("three sends of one batch stored %d rows, want 2", got)
	}
}

func TestAResendKeepsTheNewestValues(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()

	if err := db.WriteGPUMetrics([]collector.GPUMetrics{
		{NodeID: "local", GPUID: 0, Timestamp: ts, Temperature: 61},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.WriteGPUMetrics([]collector.GPUMetrics{
		{NodeID: "local", GPUID: 0, Timestamp: ts, Temperature: 77},
	}); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{GPUID: 0, NodeID: "local", From: ts - 60, To: ts + 60})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) != 1 || metrics[0].Temperature != 77 {
		t.Fatalf("want one row at 77 degrees, got %+v", metrics)
	}
}

func TestAResentHostSampleIsStoredOnce(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()
	m := &collector.HostMetrics{NodeID: "gpu-node-1", Timestamp: ts, CPUPercent: 20}

	for i := 0; i < 3; i++ {
		if err := db.WriteHostMetrics(m); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if got := countRows(t, db, "host_metrics_raw"); got != 1 {
		t.Fatalf("stored %d host rows, want 1", got)
	}
}

func TestAResentVLLMSampleIsStoredOnce(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()
	m := &collector.VLLMMetrics{NodeID: "gpu-node-1", Timestamp: ts, ModelName: "qwen", TokenThroughput: 42}

	for i := 0; i < 3; i++ {
		if err := db.WriteVLLMMetrics(m); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if got := countRows(t, db, "vllm_metrics_raw"); got != 1 {
		t.Fatalf("stored %d vllm rows, want 1", got)
	}
}

func TestAResentProcessSnapshotIsStoredOnce(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()
	procs := []collector.GPUProcess{
		{NodeID: "gpu-node-1", GPUID: 0, Timestamp: ts, PID: 100, Name: "train", GPUMem: 2048},
		{NodeID: "gpu-node-1", GPUID: 0, Timestamp: ts, PID: 200, Name: "serve", GPUMem: 512},
	}

	for i := 0; i < 3; i++ {
		if err := db.WriteGPUProcesses(procs); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if got := countRows(t, db, "gpu_processes"); got != 2 {
		t.Fatalf("stored %d process rows, want 2", got)
	}
}

// Rows written before the constraint existed have to be folded together, or
// the index cannot be created at all.
func TestTheMigrationFoldsExistingDuplicates(t *testing.T) {
	dir := t.TempDir()

	// A database at version 7: the tables exist, the unique indexes do not.
	db, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ts := time.Now().Unix()
	if _, err := db.conn.Exec("DROP INDEX IF EXISTS uq_gpu_raw"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.conn.Exec(
			`INSERT INTO gpu_metrics_raw (ts, node_id, gpu_id, gpu_util) VALUES (?, 'local', 0, ?)`,
			ts, 10*i); err != nil {
			t.Fatalf("seed duplicate: %v", err)
		}
	}
	if got := countRows(t, db, "gpu_metrics_raw"); got != 3 {
		t.Fatalf("seeding produced %d rows, want 3", got)
	}

	// Re-running the migration folds them and puts the index back.
	if _, err := db.conn.Exec(migration008); err != nil {
		t.Fatalf("re-apply migration 008: %v", err)
	}
	if got := countRows(t, db, "gpu_metrics_raw"); got != 1 {
		t.Fatalf("after folding there are %d rows, want 1", got)
	}
	db.Close()
}

// The mask says why a card slowed down and the counters say whether its
// memory is failing: both are worth nothing if they do not survive the trip
// through storage.
func TestThrottleAndEccSurviveTheRoundTrip(t *testing.T) {
	db := openTestDBWith(t, Options{})
	ts := time.Now().Unix()

	err := db.WriteGPUMetrics([]collector.GPUMetrics{{
		NodeID: "local", GPUID: 0, Timestamp: ts, GPUUtil: 90,
		ThrottleReasons: 4 | 32, EccCorrected: 17, EccUncorrected: 2,
	}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	metrics, err := db.GetGPUMetrics(GPUMetricsQuery{GPUID: 0, NodeID: "local", From: ts - 60, To: ts + 60})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("want one row, got %d", len(metrics))
	}
	if metrics[0].ThrottleReasons != 4|32 {
		t.Fatalf("throttle mask = %d", metrics[0].ThrottleReasons)
	}
	if metrics[0].EccCorrected != 17 || metrics[0].EccUncorrected != 2 {
		t.Fatalf("ecc counters = %d/%d", metrics[0].EccCorrected, metrics[0].EccUncorrected)
	}
	if !metrics[0].Throttled() {
		t.Fatal("a stored mask with a power cap in it does not read as throttled")
	}

	latest, err := db.GetLatestGPUMetrics()
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 1 || latest[0].ThrottleReasons != 4|32 {
		t.Fatalf("the status snapshot lost the mask: %+v", latest)
	}
}

func TestDeviceCapabilitiesSurviveRegistration(t *testing.T) {
	db := openTestDBWith(t, Options{})

	err := db.RegisterGPUDevices("local", []collector.GPUDevice{{
		ID: 0, UUID: "GPU-1", Name: "A100", MemTotal: 81920, DriverVer: "550",
		EccSupported: true, ThrottleSupported: true,
	}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	devices, err := db.GetGPUDevices("local")
	if err != nil {
		t.Fatalf("read devices: %v", err)
	}
	if len(devices) != 1 || !devices[0].EccSupported || !devices[0].ThrottleSupported {
		t.Fatalf("capabilities lost: %+v", devices)
	}
}
