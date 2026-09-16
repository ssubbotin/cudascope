package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/alerts"
)

func gpuPtr(id int) *int { return &id }

func sampleEvent(node string, gpu *int, kind alerts.Kind, startedAt int64) alerts.Event {
	return alerts.Event{
		NodeID:    node,
		GPUID:     gpu,
		Kind:      kind,
		Threshold: 80,
		StartedAt: startedAt,
		PeakValue: 91,
		LastValue: 88,
	}
}

// One open event per node, GPU and kind is the invariant the evaluator
// relies on. Keeping it in the database means a second process, or a retry
// after a failed write, cannot quietly produce two.
func TestASecondOpenEventForOneKeyIsRejected(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now)); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now+1)); err == nil {
		t.Fatal("a second open event for the same key was accepted")
	}

	// A different GPU, a different kind and a different node are all
	// separate keys.
	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(1), alerts.KindTemperature, now)); err != nil {
		t.Fatalf("second gpu: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindGPUUtil, now)); err != nil {
		t.Fatalf("second kind: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("other", gpuPtr(0), alerts.KindTemperature, now)); err != nil {
		t.Fatalf("second node: %v", err)
	}
}

// Node-level events carry no GPU, and NULL is not equal to NULL in an
// index, so they need the same protection through a different route.
func TestASecondOpenNodeEventIsRejected(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	if _, err := db.OpenAlertEvent(sampleEvent("local", nil, alerts.KindNodeSilent, now)); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("local", nil, alerts.KindNodeSilent, now+1)); err == nil {
		t.Fatal("a second open node event was accepted")
	}
}

func TestClosingFreesTheKey(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	id, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.CloseAlertEvent(id, now+60, 70, 95); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now+120)); err != nil {
		t.Fatalf("reopening after a close: %v", err)
	}
}

func TestOpenAlertEventsReturnsOnlyOpenOnes(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	closed, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.CloseAlertEvent(closed, now+60, 70, 95); err != nil {
		t.Fatalf("close: %v", err)
	}
	open, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(1), alerts.KindGPUUtil, now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got, err := db.OpenAlertEvents()
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	if len(got) != 1 || got[0].ID != open {
		t.Fatalf("want only the open event %d, got %+v", open, got)
	}
	if got[0].GPUID == nil || *got[0].GPUID != 1 {
		t.Fatalf("gpu id did not survive the round trip: %+v", got[0])
	}
	if got[0].EndedAt != nil {
		t.Fatalf("an open event came back with an end: %+v", got[0])
	}
}

func TestUpdateAlertEventKeepsTheRunningValues(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	id, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.UpdateAlertEvent(id, 84, 97); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := db.OpenAlertEvents()
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	if len(got) != 1 || got[0].PeakValue != 97 || got[0].LastValue != 84 {
		t.Fatalf("running values were not stored: %+v", got)
	}
}

func TestListAlertEventsHonoursRangeNodeAndLimit(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	// Three closed events an hour apart, two nodes.
	for i, spec := range []struct {
		node string
		at   int64
	}{
		{"local", now - 3*3600},
		{"local", now - 2*3600},
		{"other", now - 1*3600},
	} {
		id, err := db.OpenAlertEvent(sampleEvent(spec.node, gpuPtr(i), alerts.KindTemperature, spec.at))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := db.CloseAlertEvent(id, spec.at+60, 70, 95); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	all, err := db.ListAlertEvents(AlertEventQuery{From: now - 4*3600, To: now, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 events in range, got %d", len(all))
	}
	if all[0].StartedAt < all[1].StartedAt {
		t.Fatalf("want newest first, got %d then %d", all[0].StartedAt, all[1].StartedAt)
	}

	narrow, err := db.ListAlertEvents(AlertEventQuery{From: now - 150*60, To: now, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(narrow) != 2 {
		t.Fatalf("want 2 events in the narrow range, got %d", len(narrow))
	}

	byNode, err := db.ListAlertEvents(AlertEventQuery{From: now - 4*3600, To: now, NodeID: "other", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(byNode) != 1 || byNode[0].NodeID != "other" {
		t.Fatalf("node filter returned %+v", byNode)
	}

	limited, err := db.ListAlertEvents(AlertEventQuery{From: now - 4*3600, To: now, Limit: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("want the limit honoured, got %d", len(limited))
	}
}

// An event still open has no end to compare against a cutoff, and an alert
// that has been firing for longer than the retention window is exactly the
// one nobody wants deleted.
func TestRetentionKeepsOpenAlertEvents(t *testing.T) {
	db := openTestDB(t)
	old := time.Now().Add(-200 * 24 * time.Hour).Unix()

	staleClosed, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, old))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.CloseAlertEvent(staleClosed, old+60, 70, 95); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(1), alerts.KindTemperature, old)); err != nil {
		t.Fatalf("open: %v", err)
	}

	db.doRetention(RetentionConfig{
		Raw:    24 * time.Hour,
		M1:     30 * 24 * time.Hour,
		H1:     365 * 24 * time.Hour,
		Alerts: 90 * 24 * time.Hour,
	})

	events, err := db.ListAlertEvents(AlertEventQuery{From: old - 3600, To: time.Now().Unix(), Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want only the open event left, got %d", len(events))
	}
	if events[0].EndedAt != nil {
		t.Fatalf("the surviving event is the closed one: %+v", events[0])
	}
}

func TestNodeHeartbeatsReportEveryNodeWithItsGPUCount(t *testing.T) {
	db := openTestDB(t)
	if err := db.RegisterNode("gpu-node-1", "gpu-node-1", 4); err != nil {
		t.Fatalf("register: %v", err)
	}

	seen, err := db.NodeHeartbeats()
	if err != nil {
		t.Fatalf("node heartbeats: %v", err)
	}

	byNode := make(map[string]alerts.NodeHeartbeat, len(seen))
	for _, n := range seen {
		byNode[n.NodeID] = n
	}

	if _, ok := byNode["local"]; !ok {
		t.Fatalf("local node missing from %+v", seen)
	}
	agent, ok := byNode["gpu-node-1"]
	if !ok || agent.LastSeen == 0 {
		t.Fatalf("registered node missing or never seen: %+v", seen)
	}
	if agent.GPUCount != 4 {
		t.Fatalf("gpu count = %d, want 4", agent.GPUCount)
	}
}

// A window asks "what was alerting during this period", so an event that
// began before it and ended inside it belongs in the answer.
func TestListAlertEventsCoversEventsThatStartedEarlier(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	longRun, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now-25*3600))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.CloseAlertEvent(longRun, now-3600, 70, 95); err != nil {
		t.Fatalf("close: %v", err)
	}

	stillOpen, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(1), alerts.KindGPUUtil, now-48*3600))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	events, err := db.ListAlertEvents(AlertEventQuery{From: now - 24*3600, To: now, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[int64]bool{}
	for _, e := range events {
		seen[e.ID] = true
	}
	if !seen[longRun] {
		t.Fatalf("an event that ended inside the window is missing: %+v", events)
	}
	if !seen[stillOpen] {
		t.Fatalf("an event still open is missing: %+v", events)
	}
}

// An alert that ended before the window began is out of it.
func TestListAlertEventsLeavesOutWhatEndedEarlier(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()

	old, err := db.OpenAlertEvent(sampleEvent("local", gpuPtr(0), alerts.KindTemperature, now-72*3600))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.CloseAlertEvent(old, now-48*3600, 70, 95); err != nil {
		t.Fatalf("close: %v", err)
	}

	events, err := db.ListAlertEvents(AlertEventQuery{From: now - 24*3600, To: now, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want nothing in the window, got %+v", events)
	}
}
