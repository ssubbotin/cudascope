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

// Standalone watches its own node through its collection, which is what
// collector_stalled reports. Judging the same node by its heartbeat as well
// raised two alerts about one stalled collector.
func TestTheLocalNodeIsNotWatchedTwice(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now) // LocalNodeID is "local"

	store.lastSeen["local"] = c.now().Unix()
	store.lastSeen["gpu-node-1"] = c.now().Unix()
	store.latestTs["local"] = c.now().Unix()

	c.advance(10 * time.Minute)
	e.Sweep()

	kinds := map[Kind]int{}
	for _, ev := range e.Active() {
		kinds[ev.Kind]++
		if ev.Kind == KindNodeSilent && ev.NodeID == "local" {
			t.Fatalf("the local node raised a silence alert as well: %+v", ev)
		}
	}
	if kinds[KindCollectorStalled] != 1 {
		t.Fatalf("want the stall reported once, got %v", kinds)
	}
	if kinds[KindNodeSilent] != 1 {
		t.Fatalf("want the remote node still watched, got %v", kinds)
	}
}

// Turning a threshold off must not strand whatever it had open: the event
// would otherwise stay in the journal, open, for ever.
func TestRestoreClosesEventsOfADisabledKind(t *testing.T) {
	c, store := newClock(), newStore()
	gpu := 0
	store.restore = []Event{{
		ID: 7, NodeID: "local", GPUID: &gpu, Kind: KindTemperature,
		Threshold: 80, StartedAt: c.now().Unix() - 300, PeakValue: 91, LastValue: 88,
	}}

	cfg := testConfig()
	cfg.TempMax = 0 // the operator turned temperature alerting off
	e := New(cfg, store, c.now)
	if err := e.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := len(e.Active()); got != 0 {
		t.Fatalf("a disabled kind kept %d events open", got)
	}
	if store.closedAt[7] == 0 {
		t.Fatal("the stranded event was not closed in the store")
	}
}

// An Xid is how the driver says a card just had a fault. It arrives as an
// event rather than a reading, so it has no threshold to cross: the journal
// entry opens on the first one and closes once the card has been quiet.
func TestAnXidOpensAnEventAndQuietClosesIt(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.NoteXid("local", 0, 79)

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want one event, got %d", len(active))
	}
	if active[0].Kind != KindXid {
		t.Fatalf("unexpected kind: %+v", active[0])
	}
	if active[0].LastValue != 79 {
		t.Fatalf("want the Xid code kept, got %v", active[0].LastValue)
	}
	if active[0].GPUID == nil || *active[0].GPUID != 0 {
		t.Fatalf("want the event tied to the card: %+v", active[0])
	}
	if store.openCount() != 1 {
		t.Fatalf("the Xid was not recorded: %d rows", store.openCount())
	}

	// Still within the quiet period.
	c.advance(30 * time.Second)
	e.Sweep()
	if len(e.Active()) != 1 {
		t.Fatal("the event closed while the card was still inside the quiet window")
	}

	c.advance(31 * time.Second)
	e.Sweep()
	if got := len(e.Active()); got != 0 {
		t.Fatalf("the event stayed open after a quiet minute: %d", got)
	}
	if store.closedCount() != 1 {
		t.Fatalf("the event was not closed in the store: %d", store.closedCount())
	}
}

// A card in trouble emits Xids in bursts. One journal entry counting them
// beats a hundred entries nobody reads.
func TestRepeatedXidsStayOneEventAndAreCounted(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.NoteXid("local", 0, 13)
	c.advance(10 * time.Second)
	e.NoteXid("local", 0, 31)
	c.advance(10 * time.Second)
	e.NoteXid("local", 0, 31)

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want one event, got %d", len(active))
	}
	if active[0].PeakValue != 3 {
		t.Fatalf("want three errors counted, got %v", active[0].PeakValue)
	}
	if active[0].LastValue != 31 {
		t.Fatalf("want the newest code, got %v", active[0].LastValue)
	}
	if store.openCount() != 1 {
		t.Fatalf("a burst opened %d events", store.openCount())
	}
}

func TestXidsOnDifferentCardsAreDifferentEvents(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.NoteXid("local", 0, 79)
	e.NoteXid("local", 1, 79)

	if got := len(e.Active()); got != 2 {
		t.Fatalf("want an event per card, got %d", got)
	}
	if store.openCount() != 2 {
		t.Fatalf("want two rows, got %d", store.openCount())
	}
}

func TestAnXidFromAnUnidentifiedCardIsANodeEvent(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(testConfig(), store, c.now)

	e.NoteXid("local", -1, 48)

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want one event, got %d", len(active))
	}
	if active[0].GPUID != nil {
		t.Fatalf("want a node level event, got gpu %d", *active[0].GPUID)
	}
	if store.openCount() != 1 {
		t.Fatalf("want it recorded, got %d rows", store.openCount())
	}
}

// throttleSample is a reading whose clocks are being held back for the
// given NVML reasons.
func throttleSample(mask uint64) []collector.GPUMetrics {
	return []collector.GPUMetrics{{NodeID: "local", GPUID: 0, Temperature: 60, ThrottleReasons: mask}}
}

const (
	maskIdle      = 1  // the card has nothing to do
	maskPowerCap  = 4  // holding back to stay inside the power budget
	maskSwThermal = 32 // holding back to stay inside the temperature budget
)

func throttleConfig() Config {
	cfg := testConfig()
	cfg.Throttle = true
	return cfg
}

// A card that holds itself back is the answer to "why is this job slower
// than yesterday". The mask was collected and shown on the card from the
// start, and never judged, so the journal stayed empty while the dashboard
// said "throttled".
func TestThrottlingOpensAnEventAfterTheDwell(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(throttleConfig(), store, c.now)

	e.Observe(throttleSample(maskPowerCap))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("event opened before the dwell elapsed: %d", got)
	}

	c.advance(30 * time.Second)
	e.Observe(throttleSample(maskPowerCap))

	active := e.Active()
	if len(active) != 1 || active[0].Kind != KindThrottled {
		t.Fatalf("want one throttle event, got %+v", active)
	}
	if active[0].LastValue != maskPowerCap {
		t.Fatalf("want the reason mask kept, got %v", active[0].LastValue)
	}
	if store.openCount() != 1 {
		t.Fatalf("want it recorded, got %d rows", store.openCount())
	}

	// Clocks come back, and after the clear dwell the event closes.
	e.Observe(throttleSample(0))
	c.advance(61 * time.Second)
	e.Observe(throttleSample(0))
	if got := len(e.Active()); got != 0 {
		t.Fatalf("event stayed open after the card was free again: %d", got)
	}
}

// Idle is a state, not a slowdown. Reporting it would mark every quiet GPU
// as a problem.
func TestAnIdleCardIsNotThrottling(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(throttleConfig(), store, c.now)

	e.Observe(throttleSample(maskIdle))
	c.advance(time.Hour)
	e.Observe(throttleSample(maskIdle))

	if got := len(e.Active()); got != 0 {
		t.Fatalf("an idle card raised %d events", got)
	}
	if store.openCount() != 0 {
		t.Fatalf("an idle card recorded %d rows", store.openCount())
	}
}

// One event covers a stretch during which the reason can change, so the
// journal has to name every reason seen rather than the last one or the
// numerically largest.
func TestEveryReasonSeenDuringTheEventIsKept(t *testing.T) {
	c, store := newClock(), newStore()
	e := New(throttleConfig(), store, c.now)

	e.Observe(throttleSample(maskPowerCap))
	c.advance(30 * time.Second)
	e.Observe(throttleSample(maskPowerCap))

	c.advance(10 * time.Second)
	e.Observe(throttleSample(maskSwThermal))

	active := e.Active()
	if len(active) != 1 {
		t.Fatalf("want one event, got %d", len(active))
	}
	peak := uint64(active[0].PeakValue)
	if peak&maskPowerCap == 0 {
		t.Fatalf("the power cap was dropped from the event: mask %d", peak)
	}
	if peak&maskSwThermal == 0 {
		t.Fatalf("the thermal reason was dropped from the event: mask %d", peak)
	}
	if active[0].LastValue != maskSwThermal {
		t.Fatalf("want the newest reason as the last value, got %v", active[0].LastValue)
	}
}

func TestThrottleAlertsCanBeTurnedOff(t *testing.T) {
	c, store := newClock(), newStore()
	cfg := testConfig() // Throttle stays false
	e := New(cfg, store, c.now)

	e.Observe(throttleSample(maskPowerCap))
	c.advance(time.Hour)
	e.Observe(throttleSample(maskPowerCap))

	if got := len(e.Active()); got != 0 {
		t.Fatalf("a disabled kind raised %d events", got)
	}
}
