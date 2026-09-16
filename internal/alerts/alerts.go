// Package alerts turns metric samples into durable alert events.
//
// It exists as its own package because the evaluation belongs to the path
// that collects metrics, not to the HTTP handler that happens to render
// them. Before this, thresholds were checked inside /api/v1/status, so an
// unattended dashboard meant an unevaluated GPU: the count in the navbar
// was whatever had been true when the page loaded.
package alerts

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// Kind names what a event is about. The first three are threshold
// breaches carrying the measured value; the last two are silences carrying
// the age of the newest data in seconds.
type Kind string

const (
	KindTemperature      Kind = "temperature"
	KindGPUUtil          Kind = "gpu_util"
	KindMemUtil          Kind = "mem_util"
	KindNodeSilent       Kind = "node_silent"
	KindCollectorStalled Kind = "collector_stalled"
)

// nodeLevel is the GPU slot of an event that belongs to a whole node.
const nodeLevel = -1

// persistEvery bounds how often an open event's running values reach the
// store. Memory is the authority while an event is open; the row exists so
// the journal survives a restart, and writing it every second would cost a
// transaction per second per open event for no reader.
const persistEvery = 30 * time.Second

// Event is one alert from the moment it opened to the moment it cleared.
type Event struct {
	ID        int64   `json:"id"`
	NodeID    string  `json:"node_id"`
	GPUID     *int    `json:"gpu_id,omitempty"` // nil for node-level kinds
	Kind      Kind    `json:"kind"`
	Threshold float64 `json:"threshold"`
	StartedAt int64   `json:"started_at"`
	EndedAt   *int64  `json:"ended_at,omitempty"` // nil while open
	PeakValue float64 `json:"peak_value"`
	LastValue float64 `json:"last_value"`
}

// NodeHeartbeat is what the engine needs to judge a node's silence.
type NodeHeartbeat struct {
	NodeID   string
	LastSeen int64
	GPUCount int
}

// Store is the slice of storage the engine needs.
type Store interface {
	OpenAlertEvent(e Event) (int64, error)
	UpdateAlertEvent(id int64, last, peak float64) error
	CloseAlertEvent(id, endedAt int64, last, peak float64) error
	OpenAlertEvents() ([]Event, error)
	NodeHeartbeats() ([]NodeHeartbeat, error)
	LatestGPUMetricTs(nodeID string) (int64, error)
}

// Config holds thresholds and dwell times. A zero threshold disables its
// kind.
type Config struct {
	TempMax, GPUUtil, MemUtil int

	// For and Clear keep a value hovering at the threshold from filling the
	// journal: the breach has to hold for For before an event opens, and
	// normality has to hold for Clear before it closes.
	For, Clear time.Duration

	// NodeOfflineAfter and CollectStaleAfter are thresholds on age, so they
	// serve as their own dwell and For/Clear do not apply to them.
	NodeOfflineAfter  time.Duration
	CollectStaleAfter time.Duration

	// LocalNodeID names the node whose collector this process runs. Hub mode
	// leaves it empty: there is no local collection to call stalled.
	LocalNodeID string
}

type key struct {
	node string
	gpu  int
	kind Kind
}

type candidate struct {
	ev   Event
	open bool

	breachSince time.Time // zero when the value is below the threshold
	clearSince  time.Time // zero unless an open event is clearing
	lastPersist time.Time
}

// Engine evaluates samples and keeps the open events.
type Engine struct {
	cfg   Config
	store Store
	now   func() time.Time

	mu       sync.Mutex
	cands    map[key]*candidate
	onChange func()
}

// New creates an engine. now is injected so tests need no sleeping.
func New(cfg Config, store Store, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	return &Engine{
		cfg:   cfg,
		store: store,
		now:   now,
		cands: make(map[key]*candidate),
	}
}

// OnChange registers the callback fired when an event opens or closes.
func (e *Engine) OnChange(fn func()) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

// Restore adopts events a previous run left open, so a restart neither
// opens a duplicate nor strands a row open for ever.
func (e *Engine) Restore() error {
	evs, err := e.store.OpenAlertEvents()
	if err != nil {
		return err
	}

	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range evs {
		gpu := nodeLevel
		if ev.GPUID != nil {
			gpu = *ev.GPUID
		}
		e.cands[key{node: ev.NodeID, gpu: gpu, kind: ev.Kind}] = &candidate{
			ev:          ev,
			open:        true,
			lastPersist: now,
		}
	}
	if len(evs) > 0 {
		log.Printf("alerts: adopted %d event(s) left open by the previous run", len(evs))
	}
	return nil
}

// Observe evaluates one batch of GPU samples. Called from the collection
// path: the local collector in standalone mode, the ingest handler in hub
// mode.
func (e *Engine) Observe(metrics []collector.GPUMetrics) {
	if len(metrics) == 0 {
		return
	}

	now := e.now()
	changed := false

	e.mu.Lock()
	for _, m := range metrics {
		node := m.NodeID
		if node == "" {
			node = "local"
		}
		if e.cfg.TempMax > 0 {
			k := key{node: node, gpu: m.GPUID, kind: KindTemperature}
			changed = e.evaluate(k, float64(m.Temperature), float64(e.cfg.TempMax), now, e.cfg.For, e.cfg.Clear) || changed
		}
		if e.cfg.GPUUtil > 0 {
			k := key{node: node, gpu: m.GPUID, kind: KindGPUUtil}
			changed = e.evaluate(k, m.GPUUtil, float64(e.cfg.GPUUtil), now, e.cfg.For, e.cfg.Clear) || changed
		}
		if e.cfg.MemUtil > 0 {
			k := key{node: node, gpu: m.GPUID, kind: KindMemUtil}
			changed = e.evaluate(k, m.MemUtil, float64(e.cfg.MemUtil), now, e.cfg.For, e.cfg.Clear) || changed
		}
	}
	e.mu.Unlock()

	if changed {
		e.announce()
	}
}

// Sweep raises the events that no incoming sample can raise: a node that
// stopped reporting, and local collection that stopped producing.
func (e *Engine) Sweep() {
	now := e.now()
	changed := false

	if seen, err := e.store.NodeHeartbeats(); err != nil {
		log.Printf("alerts: read node heartbeats: %v", err)
	} else if e.cfg.NodeOfflineAfter > 0 {
		e.mu.Lock()
		for _, n := range seen {
			if n.LastSeen <= 0 {
				continue
			}
			if n.GPUCount == 0 {
				// A node with no registered GPU has nothing to report and
				// nothing to be silent about. The row migration 003 creates
				// for standalone mode is exactly that on a hub, and it used
				// to raise an alert about a node that does not exist.
				continue
			}
			k := key{node: n.NodeID, gpu: nodeLevel, kind: KindNodeSilent}
			age := float64(now.Unix() - n.LastSeen)
			changed = e.evaluate(k, age, e.cfg.NodeOfflineAfter.Seconds(), now, 0, 0) || changed
		}
		e.mu.Unlock()
	}

	if e.cfg.LocalNodeID != "" && e.cfg.CollectStaleAfter > 0 {
		ts, err := e.store.LatestGPUMetricTs(e.cfg.LocalNodeID)
		switch {
		case err != nil:
			log.Printf("alerts: read latest metric: %v", err)
		case ts == 0:
			// Nothing has been collected yet. Silence before the first
			// sample is a process still starting, not a collector stalled.
		default:
			k := key{node: e.cfg.LocalNodeID, gpu: nodeLevel, kind: KindCollectorStalled}
			age := float64(now.Unix() - ts)
			e.mu.Lock()
			changed = e.evaluate(k, age, e.cfg.CollectStaleAfter.Seconds(), now, 0, 0) || changed
			e.mu.Unlock()
		}
	}

	if changed {
		e.announce()
	}
}

// Active returns the open events, oldest first.
func (e *Engine) Active() []Event {
	e.mu.Lock()
	out := make([]Event, 0, len(e.cands))
	for _, c := range e.cands {
		if c.open {
			out = append(out, c.ev)
		}
	}
	e.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// evaluate advances one candidate and reports whether an event opened or
// closed. The caller holds e.mu.
func (e *Engine) evaluate(k key, value, threshold float64, now time.Time, forDwell, clearDwell time.Duration) bool {
	c := e.cands[k]
	if c == nil {
		if value < threshold {
			// Nothing to remember about a value that is where it belongs.
			return false
		}
		c = &candidate{}
		e.cands[k] = c
	}

	if value >= threshold {
		c.clearSince = time.Time{}

		if c.open {
			if value > c.ev.PeakValue {
				c.ev.PeakValue = value
			}
			c.ev.LastValue = value
			e.persist(c, now)
			return false
		}

		if c.breachSince.IsZero() {
			c.breachSince = now
		}
		if now.Sub(c.breachSince) < forDwell {
			return false
		}

		c.ev = Event{
			NodeID:    k.node,
			Kind:      k.kind,
			Threshold: threshold,
			StartedAt: now.Unix(),
			PeakValue: value,
			LastValue: value,
		}
		if k.gpu != nodeLevel {
			gpu := k.gpu
			c.ev.GPUID = &gpu
		}
		if id, err := e.store.OpenAlertEvent(c.ev); err != nil {
			// The alert is live either way. The row is retried by persist.
			log.Printf("alerts: record %s on %s: %v", k.kind, k.node, err)
		} else {
			c.ev.ID = id
		}
		c.open = true
		c.lastPersist = now
		log.Printf("alerts: %s on %s reached %.1f (threshold %.1f)", k.kind, k.node, value, threshold)
		return true
	}

	c.breachSince = time.Time{}
	if !c.open {
		delete(e.cands, k)
		return false
	}

	c.ev.LastValue = value
	if c.clearSince.IsZero() {
		c.clearSince = now
	}
	if now.Sub(c.clearSince) < clearDwell {
		return false
	}

	end := now.Unix()
	if c.ev.ID != 0 {
		if err := e.store.CloseAlertEvent(c.ev.ID, end, c.ev.LastValue, c.ev.PeakValue); err != nil {
			log.Printf("alerts: close %s on %s: %v", k.kind, k.node, err)
		}
	}
	log.Printf("alerts: %s on %s cleared after %s", k.kind, k.node, time.Duration(end-c.ev.StartedAt)*time.Second)
	delete(e.cands, k)
	return true
}

// persist pushes an open event's running values to the store, at most once
// per persistEvery. It also retries an insert that failed when the event
// opened. The caller holds e.mu.
func (e *Engine) persist(c *candidate, now time.Time) {
	if now.Sub(c.lastPersist) < persistEvery {
		return
	}
	c.lastPersist = now

	if c.ev.ID == 0 {
		id, err := e.store.OpenAlertEvent(c.ev)
		if err != nil {
			log.Printf("alerts: record %s on %s: %v", c.ev.Kind, c.ev.NodeID, err)
			return
		}
		c.ev.ID = id
		return
	}

	if err := e.store.UpdateAlertEvent(c.ev.ID, c.ev.LastValue, c.ev.PeakValue); err != nil {
		log.Printf("alerts: update %s on %s: %v", c.ev.Kind, c.ev.NodeID, err)
	}
}

func (e *Engine) announce() {
	e.mu.Lock()
	fn := e.onChange
	e.mu.Unlock()

	if fn != nil {
		fn()
	}
}

// Config returns the thresholds this engine judges by, for the API to
// report alongside the events.
func (e *Engine) Config() Config {
	return e.cfg
}
