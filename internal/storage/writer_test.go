package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.RegisterNode("local", "testhost", 1); err != nil {
		t.Fatalf("register node: %v", err)
	}
	// Pretend the process has been up for a while without the node being
	// touched since registration.
	stale := time.Now().Add(-time.Hour).Unix()
	if _, err := db.conn.Exec(`UPDATE nodes SET last_seen = ? WHERE node_id = ?`, stale, "local"); err != nil {
		t.Fatalf("age node: %v", err)
	}
	return db
}

func localNodeOnline(t *testing.T, db *DB) bool {
	t.Helper()
	nodes, err := db.GetNodes()
	if err != nil {
		t.Fatalf("get nodes: %v", err)
	}
	for _, n := range nodes {
		if n.NodeID == "local" {
			return n.Online
		}
	}
	t.Fatal("node 'local' is missing")
	return false
}

// The local collector writes to storage directly, never through the ingest
// handlers that refresh last_seen for remote agents. Without this the local
// node reports offline from 60 seconds after startup onwards, however well
// collection is going.
func TestWriteGPUMetricsMarksNodeSeen(t *testing.T) {
	db := openTestDB(t)
	if localNodeOnline(t, db) {
		t.Fatal("precondition: node should start out stale")
	}

	// NodeID is empty, exactly as the local GPU collector emits it.
	err := db.WriteGPUMetrics([]collector.GPUMetrics{{
		Timestamp: time.Now().Unix(),
		GPUID:     0,
		GPUUtil:   42,
	}})
	if err != nil {
		t.Fatalf("write gpu metrics: %v", err)
	}

	if !localNodeOnline(t, db) {
		t.Error("node still offline after GPU metrics were written for it")
	}
}

func TestWriteHostMetricsMarksNodeSeen(t *testing.T) {
	db := openTestDB(t)
	if localNodeOnline(t, db) {
		t.Fatal("precondition: node should start out stale")
	}

	err := db.WriteHostMetrics(&collector.HostMetrics{
		NodeID:    "local",
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("write host metrics: %v", err)
	}

	if !localNodeOnline(t, db) {
		t.Error("node still offline after host metrics were written for it")
	}
}
