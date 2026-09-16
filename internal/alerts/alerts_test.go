package alerts

import (
	"sync"
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// clock is an injected time source, so no test has to sleep.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Unix(1_700_000_000, 0)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeStore records what the engine persisted.
type fakeStore struct {
	mu sync.Mutex

	nextID   int64
	opened   []Event
	closedAt map[int64]int64
	updates  int

	restore  []Event
	lastSeen map[string]int64
	noGPUs   map[string]bool
	latestTs map[string]int64

	openErr error
}

func newStore() *fakeStore {
	return &fakeStore{
		closedAt: make(map[int64]int64),
		lastSeen: make(map[string]int64),
		noGPUs:   make(map[string]bool),
		latestTs: make(map[string]int64),
	}
}

func (s *fakeStore) OpenAlertEvent(e Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return 0, s.openErr
	}
	s.nextID++
	e.ID = s.nextID
	s.opened = append(s.opened, e)
	return e.ID, nil
}

func (s *fakeStore) UpdateAlertEvent(id int64, last, peak float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates++
	return nil
}

func (s *fakeStore) CloseAlertEvent(id, endedAt int64, last, peak float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closedAt[id] = endedAt
	return nil
}

func (s *fakeStore) OpenAlertEvents() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restore, nil
}

func (s *fakeStore) NodeHeartbeats() ([]NodeHeartbeat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NodeHeartbeat, 0, len(s.lastSeen))
	for node, ts := range s.lastSeen {
		gpus := 1
		if s.noGPUs[node] {
			gpus = 0
		}
		out = append(out, NodeHeartbeat{NodeID: node, LastSeen: ts, GPUCount: gpus})
	}
	return out, nil
}

func (s *fakeStore) LatestGPUMetricTs(nodeID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestTs[nodeID], nil
}

func (s *fakeStore) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.opened)
}

func (s *fakeStore) closedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.closedAt)
}

func testConfig() Config {
	return Config{
		TempMax:           80,
		For:               30 * time.Second,
		Clear:             60 * time.Second,
		NodeOfflineAfter:  60 * time.Second,
		CollectStaleAfter: time.Minute,
		LocalNodeID:       "local",
	}
}

// sample builds a one-GPU reading at the given temperature.
func sample(temp int) []collector.GPUMetrics {
	return []collector.GPUMetrics{{NodeID: "local", GPUID: 0, Temperature: temp}}
}

func TestOpensOnlyAfterTheDwellHasElapsed(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.Observe(sample(85))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("event opened before the dwell elapsed: %d active", got)
	}

	c.advance(29 * time.Second)
	e.Observe(sample(85))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("event opened one second early: %d active", got)
	}

	c.advance(time.Second)
	e.Observe(sample(85))

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want 1 active event after the dwell, got %d", len(active))
	}
	if active[0].Kind != KindTemperature || active[0].PeakValue != 85 {
		t.Fatalf("unexpected event: %+v", active[0])
	}
	if store.openCount() != 1 {
		t.Fatalf("want 1 persisted event, got %d", store.openCount())
	}
}

func TestABriefSpikeOpensNothing(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.Observe(sample(95))
	c.advance(10 * time.Second)
	e.Observe(sample(95))
	c.advance(10 * time.Second)
	e.Observe(sample(60))

	c.advance(time.Hour)
	e.Observe(sample(60))

	if got := len(e.Active()); got != 0 {
		t.Fatalf("a 20 second spike opened %d events", got)
	}
	if store.openCount() != 0 {
		t.Fatalf("a 20 second spike persisted %d events", store.openCount())
	}
}

func TestClosesOnlyAfterNormalityHolds(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	openEvent(t, e, c)

	c.advance(time.Second)
	e.Observe(sample(60))
	if len(e.Active()) != 1 {
		t.Fatal("event closed immediately on the first normal sample")
	}

	c.advance(59 * time.Second)
	e.Observe(sample(60))
	if len(e.Active()) != 1 {
		t.Fatal("event closed one second early")
	}

	c.advance(time.Second)
	e.Observe(sample(60))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("event stayed open past the clear dwell: %d active", got)
	}
	if store.closedCount() != 1 {
		t.Fatalf("want 1 closed event, got %d", store.closedCount())
	}
}

func TestABreachDuringTheClearTimerKeepsOneEvent(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	openEvent(t, e, c)
	opened := e.Active()[0]

	// Drop below the threshold, then come back before the clear dwell ends.
	c.advance(30 * time.Second)
	e.Observe(sample(60))
	c.advance(30 * time.Second)
	e.Observe(sample(85))

	// Well past the clear dwell measured from the first normal sample.
	c.advance(90 * time.Second)
	e.Observe(sample(85))

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want the same single event, got %d", len(active))
	}
	if active[0].ID != opened.ID || active[0].StartedAt != opened.StartedAt {
		t.Fatalf("flapping replaced the event: was %+v, now %+v", opened, active[0])
	}
	if store.openCount() != 1 {
		t.Fatalf("flapping persisted %d events", store.openCount())
	}
	if store.closedCount() != 0 {
		t.Fatalf("flapping closed the event %d times", store.closedCount())
	}
}

func TestPeakSurvivesALowerSample(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	openEvent(t, e, c)

	c.advance(time.Second)
	e.Observe(sample(97))
	c.advance(time.Second)
	e.Observe(sample(84))

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want 1 active event, got %d", len(active))
	}
	if active[0].PeakValue != 97 {
		t.Fatalf("peak = %v, want 97", active[0].PeakValue)
	}
	if active[0].LastValue != 84 {
		t.Fatalf("last = %v, want 84", active[0].LastValue)
	}
}

func TestRestoreAdoptsAnEventLeftOpen(t *testing.T) {
	c, store := newClock(), newStore()
	gpu := 0
	store.restore = []Event{{
		ID: 42, NodeID: "local", GPUID: &gpu, Kind: KindTemperature,
		Threshold: 80, StartedAt: c.now().Unix() - 600, PeakValue: 91, LastValue: 88,
	}}

	e := New(testConfig(), store, c.now)
	if err := e.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := len(e.Active()); got != 1 {
		t.Fatalf("want the open event adopted, got %d active", got)
	}

	// The same breach continuing must not open a second event.
	e.Observe(sample(85))
	c.advance(time.Minute)
	e.Observe(sample(85))

	if store.openCount() != 0 {
		t.Fatalf("restore opened %d new events", store.openCount())
	}
	if e.Active()[0].ID != 42 {
		t.Fatalf("adopted event lost its id: %+v", e.Active()[0])
	}

	// And it closes normally once the breach ends.
	e.Observe(sample(50))
	c.advance(61 * time.Second)
	e.Observe(sample(50))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("adopted event never closed: %d active", got)
	}
	if store.closedAt[42] == 0 {
		t.Fatal("adopted event was not closed in the store")
	}
}

func TestASilentNodeOpensAndItsReturnCloses(t *testing.T) {
	c, store := newClock(), newStore()
	cfg := testConfig()
	cfg.LocalNodeID = "" // hub mode: no local collector to watch
	e := New(cfg, store, c.now)

	store.lastSeen["gpu-node-1"] = c.now().Unix()
	e.Sweep()
	if got := len(e.Active()); got != 0 {
		t.Fatalf("a fresh node opened %d events", got)
	}

	c.advance(61 * time.Second)
	e.Sweep()

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want 1 silence event, got %d", len(active))
	}
	if active[0].Kind != KindNodeSilent || active[0].NodeID != "gpu-node-1" {
		t.Fatalf("unexpected event: %+v", active[0])
	}
	if active[0].GPUID != nil {
		t.Fatalf("a node level event carries a gpu id: %+v", active[0])
	}

	store.lastSeen["gpu-node-1"] = c.now().Unix()
	e.Sweep()
	if got := len(e.Active()); got != 0 {
		t.Fatalf("the node came back and %d events stayed open", got)
	}
}

func TestStalledLocalCollectionOpensAnEvent(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	// Nothing collected yet: silence before the first sample is not a stall.
	e.Sweep()
	if got := len(e.Active()); got != 0 {
		t.Fatalf("an empty store opened %d events", got)
	}

	store.latestTs["local"] = c.now().Unix()
	e.Sweep()
	if got := len(e.Active()); got != 0 {
		t.Fatalf("fresh collection opened %d events", got)
	}

	c.advance(61 * time.Second)
	e.Sweep()

	active := e.Active()
	if len(active) != 1 || active[0].Kind != KindCollectorStalled {
		t.Fatalf("want a collector_stalled event, got %+v", active)
	}
}

func TestAZeroThresholdDisablesItsKind(t *testing.T) {
	c, store := newClock(), newStore()
	cfg := testConfig()
	cfg.TempMax = 0
	e := New(cfg, store, c.now)

	e.Observe(sample(120))
	c.advance(time.Hour)
	e.Observe(sample(120))

	if got := len(e.Active()); got != 0 {
		t.Fatalf("a disabled kind opened %d events", got)
	}
}

func TestChangesAreAnnouncedOnce(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	var changes int
	var mu sync.Mutex
	e.OnChange(func() {
		mu.Lock()
		changes++
		mu.Unlock()
	})

	openEvent(t, e, c)

	// Steady breach: no further transitions to announce.
	c.advance(time.Second)
	e.Observe(sample(90))

	mu.Lock()
	got := changes
	mu.Unlock()
	if got != 1 {
		t.Fatalf("want 1 change announcement, got %d", got)
	}
}

func TestObserveIsSafeFromMultipleGoroutines(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				e.Observe(sample(85))
				e.Active()
			}
		}()
	}
	wg.Wait()
}

// openEvent drives the engine past the dwell so an event is open.
func openEvent(t *testing.T, e *Engine, c *clock) {
	t.Helper()
	e.Observe(sample(85))
	c.advance(30 * time.Second)
	e.Observe(sample(85))
	if len(e.Active()) != 1 {
		t.Fatalf("setup: want 1 active event, got %d", len(e.Active()))
	}
}

// A hub inherits a "local" row from the standalone migration and has no
// local collector. Alerting about that node meant every hub reported a
// silent node it never had.
func TestANodeWithNoRegisteredGPUsIsNotWatched(t *testing.T) {
	c, store := newClock(), newStore()
	cfg := testConfig()
	cfg.LocalNodeID = ""
	e := New(cfg, store, c.now)

	store.lastSeen["local"] = c.now().Unix()
	store.noGPUs["local"] = true

	c.advance(10 * time.Minute)
	e.Sweep()

	if got := len(e.Active()); got != 0 {
		t.Fatalf("a node with no GPUs raised %d events", got)
	}
}
