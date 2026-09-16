package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sergey/cudascope/internal/alerts"
	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/storage"
)

func newAlertServer(t *testing.T, cfg alerts.Config, stateEvery time.Duration) (*Server, *storage.DB, *alerts.Engine) {
	t.Helper()

	db, err := storage.Open(t.TempDir(), storage.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.RegisterNode("local", "testhost", 1); err != nil {
		t.Fatalf("register node: %v", err)
	}

	engine := alerts.New(cfg, db, nil)
	srv := NewServer(Options{
		Store:         db,
		Hub:           NewHub(),
		Alerts:        engine,
		StateInterval: stateEvery,
	})
	return srv, db, engine
}

func hotSample() []collector.GPUMetrics {
	return []collector.GPUMetrics{{NodeID: "local", GPUID: 0, Temperature: 95, Timestamp: time.Now().Unix()}}
}

// readState reads until a state snapshot arrives or the deadline passes.
func readState(t *testing.T, conn *websocket.Conn, within time.Duration) StateSnapshot {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(within))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if probe.Type != "state" {
			continue
		}
		var snap StateSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			t.Fatalf("unmarshal state: %v", err)
		}
		return snap
	}
}

// An open tab used to learn about nodes, devices and alerts exactly once,
// from the single /api/v1/status call made at mount.
func TestStateReachesOpenTabsWhenAnAlertOpens(t *testing.T) {
	srv, _, engine := newAlertServer(t, alerts.Config{TempMax: 80, NodeOfflineAfter: time.Minute}, time.Hour)

	wsSrv := httptest.NewServer(http.HandlerFunc(srv.hub.HandleWS))
	defer wsSrv.Close()
	conn := dialHub(t, wsSrv)
	defer conn.Close()
	waitForClients(t, srv.hub, 1, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunStateBroadcast(ctx)

	engine.Observe(hotSample())

	snap := readState(t, conn, 3*time.Second)
	if len(snap.Alerts) != 1 {
		t.Fatalf("want the open alert in the snapshot, got %+v", snap.Alerts)
	}
	if snap.Alerts[0].Kind != alerts.KindTemperature || snap.Alerts[0].PeakValue != 95 {
		t.Fatalf("unexpected alert: %+v", snap.Alerts[0])
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].NodeID != "local" {
		t.Fatalf("want the node list alongside, got %+v", snap.Nodes)
	}
}

// A tab that connects between changes must not wait for the next one.
func TestStateIsResentOnTheTicker(t *testing.T) {
	srv, _, _ := newAlertServer(t, alerts.Config{TempMax: 80}, 50*time.Millisecond)

	wsSrv := httptest.NewServer(http.HandlerFunc(srv.hub.HandleWS))
	defer wsSrv.Close()
	conn := dialHub(t, wsSrv)
	defer conn.Close()
	waitForClients(t, srv.hub, 1, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunStateBroadcast(ctx)

	snap := readState(t, conn, 3*time.Second)
	if len(snap.Nodes) != 1 {
		t.Fatalf("want the node list on the periodic snapshot, got %+v", snap.Nodes)
	}
}

func TestAlertsEndpointReportsOpenEvents(t *testing.T) {
	srv, _, engine := newAlertServer(t, alerts.Config{TempMax: 80}, time.Hour)
	engine.Observe(hotSample())

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var body struct {
		Config map[string]int `json:"config"`
		Alerts []alerts.Event `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Alerts) != 1 || body.Alerts[0].Kind != alerts.KindTemperature {
		t.Fatalf("want one open temperature event, got %+v", body.Alerts)
	}
	if body.Config["temp_max"] != 80 {
		t.Fatalf("config not reported: %+v", body.Config)
	}
}

func TestAlertHistoryHonoursRangeAndLimit(t *testing.T) {
	srv, db, _ := newAlertServer(t, alerts.Config{TempMax: 80}, time.Hour)
	now := time.Now().Unix()

	for i, at := range []int64{now - 4*3600, now - 2*3600, now - 600} {
		gpu := i
		id, err := db.OpenAlertEvent(alerts.Event{
			NodeID: "local", GPUID: &gpu, Kind: alerts.KindTemperature,
			Threshold: 80, StartedAt: at, PeakValue: 90, LastValue: 90,
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := db.CloseAlertEvent(id, at+60, 70, 90); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts/history?range=3h", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var events []alerts.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want the two events inside 3h, got %d", len(events))
	}
	if events[0].StartedAt < events[1].StartedAt {
		t.Fatal("want newest first")
	}

	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts/history?range=24h&limit=1", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("limit ignored: got %d events", len(events))
	}
}

// Ingest is the collection path of a hub, so samples arriving from an agent
// must be judged the same way the local collector's are.
func TestIngestedMetricsAreEvaluated(t *testing.T) {
	srv, _, engine := newAlertServer(t, alerts.Config{TempMax: 80}, time.Hour)

	payload, err := json.Marshal(hotSampleFor("gpu-node-1"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/v1/ingest/gpu-metrics", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	active := engine.Active()
	if len(active) != 1 || active[0].NodeID != "gpu-node-1" {
		t.Fatalf("want the agent's sample evaluated, got %+v", active)
	}
}

func hotSampleFor(node string) []collector.GPUMetrics {
	return []collector.GPUMetrics{{NodeID: node, GPUID: 0, Temperature: 95, Timestamp: time.Now().Unix()}}
}

// An Xid reaches the hub over its own route, because it is an event the
// driver raised rather than a sample anybody took.
func TestAnAgentsXidLandsInTheJournal(t *testing.T) {
	srv, db, engine := newAlertServer(t, alerts.Config{TempMax: 80, Clear: time.Minute}, time.Hour)

	body := `{"node_id":"gpu-node-1","gpu_id":1,"xid":79}`
	req := httptest.NewRequest("POST", "/api/v1/ingest/xid", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	active := engine.Active()
	if len(active) != 1 || active[0].Kind != alerts.KindXid {
		t.Fatalf("want an open Xid event, got %+v", active)
	}
	if active[0].LastValue != 79 {
		t.Fatalf("want the code kept, got %v", active[0].LastValue)
	}

	events, err := db.ListAlertEvents(storage.AlertEventQuery{
		From: time.Now().Unix() - 60, To: time.Now().Unix() + 60, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 || events[0].NodeID != "gpu-node-1" {
		t.Fatalf("the journal does not hold the fault: %+v", events)
	}
}

func TestXidIngestNeedsANode(t *testing.T) {
	srv, _, _ := newAlertServer(t, alerts.Config{}, time.Hour)

	req := httptest.NewRequest("POST", "/api/v1/ingest/xid", bytes.NewReader([]byte(`{"gpu_id":0,"xid":13}`)))
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
