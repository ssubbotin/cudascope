package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func openTestDBWith(t *testing.T, opts Options) *DB {
	t.Helper()
	db, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.RegisterNode("local", "testhost", 1); err != nil {
		t.Fatalf("register node: %v", err)
	}
	return db
}

func writeGPUSample(t *testing.T, db *DB, gpuID int, ts int64, temp int) {
	t.Helper()
	err := db.WriteGPUMetrics([]collector.GPUMetrics{{
		NodeID: "local", GPUID: gpuID, Timestamp: ts, Temperature: temp, GPUUtil: 50,
	}})
	if err != nil {
		t.Fatalf("write gpu metrics: %v", err)
	}
}

// A slower collector must not empty the dashboard. The window used to be a
// hardcoded 30 seconds, so --collect-interval 60s produced a status
// response with no GPUs in it and no hint why.
func TestLatestMetricsFollowTheConfiguredWindow(t *testing.T) {
	db := openTestDBWith(t, Options{FreshWindow: 5 * time.Minute})
	writeGPUSample(t, db, 0, time.Now().Add(-2*time.Minute).Unix(), 70)

	gpus, err := db.GetLatestGPUMetrics()
	if err != nil {
		t.Fatalf("latest gpu metrics: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("want the two minute old sample, got %d rows", len(gpus))
	}
}

func TestLatestMetricsStillExpire(t *testing.T) {
	db := openTestDBWith(t, Options{FreshWindow: time.Minute})
	writeGPUSample(t, db, 0, time.Now().Add(-10*time.Minute).Unix(), 70)

	gpus, err := db.GetLatestGPUMetrics()
	if err != nil {
		t.Fatalf("latest gpu metrics: %v", err)
	}
	if len(gpus) != 0 {
		t.Fatalf("a ten minute old sample is not current: %d rows", len(gpus))
	}
}

func TestZeroOptionsKeepTheOldDefaults(t *testing.T) {
	db := openTestDBWith(t, Options{})
	writeGPUSample(t, db, 0, time.Now().Add(-10*time.Second).Unix(), 70)

	gpus, err := db.GetLatestGPUMetrics()
	if err != nil {
		t.Fatalf("latest gpu metrics: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("want the ten second old sample under the default window, got %d rows", len(gpus))
	}
}

func TestANodeGoesOfflineAfterTheConfiguredSilence(t *testing.T) {
	db := openTestDBWith(t, Options{NodeOfflineAfter: 10 * time.Second})

	stale := time.Now().Add(-30 * time.Second).Unix()
	if _, err := db.conn.Exec(`UPDATE nodes SET last_seen = ? WHERE node_id = ?`, stale, "local"); err != nil {
		t.Fatalf("age node: %v", err)
	}
	if localNodeOnline(t, db) {
		t.Fatal("a node silent for three times the threshold is online")
	}

	if err := db.UpdateNodeSeen("local"); err != nil {
		t.Fatalf("update seen: %v", err)
	}
	if !localNodeOnline(t, db) {
		t.Fatal("a node that just reported is offline")
	}
}

// The list is a snapshot of what is running now. Selecting everything seen
// in the last N seconds kept processes that had already exited, with their
// memory still counted against the GPU.
func TestLatestProcessesLeaveOutTheOnesThatExited(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	earlier := []collector.GPUProcess{
		{NodeID: "local", GPUID: 0, Timestamp: now - 5, PID: 100, Name: "train", GPUMem: 2048},
		{NodeID: "local", GPUID: 0, Timestamp: now - 5, PID: 200, Name: "notebook", GPUMem: 512},
	}
	if err := db.WriteGPUProcesses(earlier); err != nil {
		t.Fatalf("write processes: %v", err)
	}

	current := []collector.GPUProcess{
		{NodeID: "local", GPUID: 0, Timestamp: now, PID: 100, Name: "train", GPUMem: 3072},
	}
	if err := db.WriteGPUProcesses(current); err != nil {
		t.Fatalf("write processes: %v", err)
	}

	procs, err := db.GetAllGPUProcesses()
	if err != nil {
		t.Fatalf("all processes: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("want only the running process, got %+v", procs)
	}
	if procs[0].PID != 100 || procs[0].GPUMem != 3072 {
		t.Fatalf("want the newest row for pid 100, got %+v", procs[0])
	}
}

func TestLatestProcessesCoverEveryGPU(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	// GPU 1 reported one tick earlier than GPU 0, which is ordinary.
	if err := db.WriteGPUProcesses([]collector.GPUProcess{
		{NodeID: "local", GPUID: 1, Timestamp: now - 1, PID: 300, Name: "serve", GPUMem: 8192},
	}); err != nil {
		t.Fatalf("write processes: %v", err)
	}
	if err := db.WriteGPUProcesses([]collector.GPUProcess{
		{NodeID: "local", GPUID: 0, Timestamp: now, PID: 100, Name: "train", GPUMem: 3072},
	}); err != nil {
		t.Fatalf("write processes: %v", err)
	}

	procs, err := db.GetAllGPUProcesses()
	if err != nil {
		t.Fatalf("all processes: %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("want one process per GPU, got %+v", procs)
	}
}
