package storage

import (
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

func ollamaReading(ts int64, models ...collector.OllamaModel) *collector.OllamaMetrics {
	return &collector.OllamaMetrics{
		NodeID:    "local",
		Timestamp: ts,
		Version:   "0.13.0",
		Models:    models,
	}
}

// The newest tick is what "loaded now" means. Taking every row inside a
// window would show a model that has since been unloaded, with its memory
// still counted, which is the mistake the process list already made once.
func TestTheLatestOllamaReadingIsOneTick(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	if err := db.WriteOllamaMetrics(ollamaReading(now-20,
		collector.OllamaModel{Name: "gemma4:31b", SizeBytes: 20_000_000_000, VRAMBytes: 20_000_000_000})); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.WriteOllamaMetrics(ollamaReading(now,
		collector.OllamaModel{Name: "qwen3:8b", SizeBytes: 8_000_000_000, VRAMBytes: 6_000_000_000, ExpiresAt: now + 300})); err != nil {
		t.Fatalf("write: %v", err)
	}

	m, err := db.ReadLatestOllama("local")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m == nil {
		t.Fatal("want a reading")
	}
	if len(m.Models) != 1 {
		t.Fatalf("want the newest tick alone, got %d models: %+v", len(m.Models), m.Models)
	}
	if m.Models[0].Name != "qwen3:8b" {
		t.Fatalf("model = %q", m.Models[0].Name)
	}
	if m.Models[0].VRAMBytes != 6_000_000_000 || m.Models[0].ExpiresAt != now+300 {
		t.Fatalf("model came back as %+v", m.Models[0])
	}
	if m.Version != "0.13.0" {
		t.Fatalf("version = %q", m.Version)
	}
}

// A server holding nothing is a reading. Without the tick row it would be
// indistinguishable from a server that stopped answering.
func TestAnIdleOllamaIsStoredAsAReading(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	if err := db.WriteOllamaMetrics(ollamaReading(now)); err != nil {
		t.Fatalf("write: %v", err)
	}

	m, err := db.ReadLatestOllama("local")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m == nil {
		t.Fatal("an idle server still has a reading")
	}
	if len(m.Models) != 0 {
		t.Fatalf("want nothing loaded, got %+v", m.Models)
	}
}

// A server that stopped answering leaves no fresh tick, and the dashboard
// is told there is nothing rather than shown the last thing it saw.
func TestAStaleOllamaReadingIsNotCurrent(t *testing.T) {
	db := openTestDBWith(t, Options{})
	old := time.Now().Unix() - 3600

	if err := db.WriteOllamaMetrics(ollamaReading(old,
		collector.OllamaModel{Name: "qwen3:8b", SizeBytes: 8_000_000_000})); err != nil {
		t.Fatalf("write: %v", err)
	}

	m, err := db.ReadLatestOllama("local")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m != nil {
		t.Fatalf("an hour old reading was served as current: %+v", m)
	}
}

// History is one point per tick, idle ticks included, so a chart does not
// draw a busy server through the hours it was holding nothing.
func TestOllamaHistoryKeepsTheIdleTicks(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	if err := db.WriteOllamaMetrics(ollamaReading(now-30,
		collector.OllamaModel{Name: "qwen3:8b", SizeBytes: 8_000_000_000, VRAMBytes: 6_000_000_000})); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.WriteOllamaMetrics(ollamaReading(now - 20)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.WriteOllamaMetrics(ollamaReading(now-10,
		collector.OllamaModel{Name: "qwen3:8b", SizeBytes: 8_000_000_000, VRAMBytes: 8_000_000_000},
		collector.OllamaModel{Name: "nomic-embed-text", SizeBytes: 270_000_000, VRAMBytes: 270_000_000})); err != nil {
		t.Fatalf("write: %v", err)
	}

	points, err := db.ReadOllamaHistory("local", now-60, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("want three ticks, got %d: %+v", len(points), points)
	}

	if points[0].Loaded != 1 || points[0].VRAMBytes != 6_000_000_000 {
		t.Errorf("first tick = %+v", points[0])
	}
	if points[1].Loaded != 0 || points[1].VRAMBytes != 0 {
		t.Errorf("the idle tick came back as %+v", points[1])
	}
	if points[2].Loaded != 2 || points[2].VRAMBytes != 8_270_000_000 {
		t.Errorf("two models should sum: %+v", points[2])
	}
}

// A resent batch is an overwrite. Agents retry, and a retry must not double
// the memory a tick reports.
func TestResendingAnOllamaReadingOverwritesIt(t *testing.T) {
	db := openTestDBWith(t, Options{})
	now := time.Now().Unix()

	reading := ollamaReading(now, collector.OllamaModel{
		Name: "qwen3:8b", SizeBytes: 8_000_000_000, VRAMBytes: 6_000_000_000,
	})
	for range 3 {
		if err := db.WriteOllamaMetrics(reading); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	points, err := db.ReadOllamaHistory("local", now-60, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("want one tick, got %d", len(points))
	}
	if points[0].VRAMBytes != 6_000_000_000 {
		t.Fatalf("vram = %d, want it counted once", points[0].VRAMBytes)
	}
}
