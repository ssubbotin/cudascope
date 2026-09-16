package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewServer(db, NewHub(), nil, false, "", "", AlertConfig{}), db
}

func healthzCode(t *testing.T, s *Server) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/healthz", nil))
	return rec.Code
}

func writeGPUMetricAt(t *testing.T, db *storage.DB, ts time.Time) {
	t.Helper()
	err := db.WriteGPUMetrics([]collector.GPUMetrics{{
		NodeID:    "local",
		Timestamp: ts.Unix(),
		GPUID:     0,
		GPUUtil:   50,
	}})
	if err != nil {
		t.Fatalf("write gpu metrics: %v", err)
	}
}

// healthz must reflect whether metrics are still arriving. Answering "ok"
// purely because the HTTP goroutine is alive is what let the container
// report healthy for an hour with a dead collector.
func TestHealthzFailsWhenCollectionIsStale(t *testing.T) {
	s, db := newTestServer(t)
	s.SetCollectorWatchdog(30 * time.Second)

	if got := healthzCode(t, s); got != 503 {
		t.Errorf("empty store: got %d, want 503", got)
	}

	writeGPUMetricAt(t, db, time.Now().Add(-time.Hour))
	if got := healthzCode(t, s); got != 503 {
		t.Errorf("stale row: got %d, want 503", got)
	}

	writeGPUMetricAt(t, db, time.Now())
	if got := healthzCode(t, s); got != 200 {
		t.Errorf("fresh row: got %d, want 200", got)
	}
}

// Hub mode has no local collector, so the check stays off and healthz keeps
// reporting on the process alone.
func TestHealthzIgnoresStalenessWhenWatchdogIsOff(t *testing.T) {
	s, db := newTestServer(t)

	if got := healthzCode(t, s); got != 200 {
		t.Errorf("empty store: got %d, want 200", got)
	}

	writeGPUMetricAt(t, db, time.Now().Add(-time.Hour))
	if got := healthzCode(t, s); got != 200 {
		t.Errorf("stale row: got %d, want 200", got)
	}
}
