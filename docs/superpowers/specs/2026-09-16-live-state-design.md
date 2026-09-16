# Live state: alerts as events, pushed state, honest freshness

Status: approved, ready for implementation
Date: 2026-09-16
Scope: block 1 of 5 from the architecture review

## Problem

Three defects share one root cause: state that the dashboard presents as current is
computed only when something happens to ask for it.

1. **Alerts are evaluated by the HTTP layer.** `api.Server.checkAlerts` runs inside
   `handleStatus` and inside the agent ingest handler. In standalone mode the collector
   writes to storage directly and never evaluates anything, so `/api/v1/alerts` reflects
   whatever was true the last time somebody loaded `/api/v1/status`. The UI calls
   `fetchStatus()` once, at mount. An open tab therefore shows the alert count from page
   load forever, and an overheating GPU at 03:00 alerts nobody.
2. **Node and device lists have the same lifetime.** `online` is computed server-side in
   `GetNodes` at query time and delivered once. A node that dies stays green in an open tab.
3. **"Current" is a hardcoded 30 second window.** Five queries in `storage/reader.go` select
   rows newer than `now - 30`. With `--collect-interval 60s` the dashboard shows no GPUs at
   all, for no visible reason. `GetAllGPUProcesses` uses the same window as a stand-in for
   "the latest snapshot", so a process that exited 25 seconds ago still occupies the table.

Alerts also have no memory. There is no record that a GPU sat at 87 C for six minutes last
night, and a node that stops answering produces no record at all.

## Goals

- Alert state is computed where metrics enter the system, on every sample, in every mode.
- Every alert transition is durable: a journal answers "what fired, when, how high, how long".
- Silence is a first-class event: a node that stops reporting and a collector that stalls
  both open events.
- Nodes, devices and alerts reach open browser tabs without polling.
- "Current" derives from the configured collection interval.

## Non-goals (later blocks)

Notification delivery (webhook, mail, messenger), per-GPU alert thresholds, alert
acknowledgement, agent-side evaluation, and the history-cost work of block 4.

## Design

### `internal/alerts`

A self-contained evaluator, testable without a database and without HTTP, in the shape that
`internal/watchdog` already established.

```go
type Config struct {
    TempMax, GPUUtil, MemUtil int           // 0 disables that kind
    For, Clear                time.Duration // dwell before opening / before closing
    NodeOfflineAfter          time.Duration // silence that counts as offline
    CollectStaleAfter         time.Duration // local collection age that counts as stalled
    LocalNodeID               string        // empty in hub mode: no local collector to watch
}

type Engine struct{ /* ... */ }

func New(cfg Config, store Store, now func() time.Time) *Engine

func (e *Engine) Restore() error                        // adopt events left open by a previous run
func (e *Engine) Observe(metrics []collector.GPUMetrics) // called from the collection path
func (e *Engine) Sweep()                                 // ticker: silence and stall events
func (e *Engine) Active() []Event
func (e *Engine) OnChange(fn func())
```

`Store` is the narrow slice of `storage.DB` the engine needs:

```go
type Store interface {
    OpenAlertEvent(e Event) (int64, error)
    UpdateAlertEvent(id int64, last, peak float64, ts int64) error
    CloseAlertEvent(id int64, endedAt int64) error
    OpenAlertEvents() ([]Event, error)
    NodeLastSeen() (map[string]int64, error)
    LatestGPUMetricTs(nodeID string) (int64, error)
}
```

Time is injected, so every test runs without sleeping.

### Event

```go
type Kind string // temperature | gpu_util | mem_util | node_silent | collector_stalled

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
```

For `node_silent` and `collector_stalled` the threshold and the values carry seconds of age,
which keeps one table and one shape for every kind.

### Transitions

A candidate is keyed by node, GPU and kind.

- A sample at or above the threshold starts a dwell timer. The event opens once the breach
  has held for `For`.
- A sample below the threshold starts a clear timer. The event closes once normality has
  held for `Clear`.
- A breach arriving before `Clear` elapses cancels the clear timer and keeps the same event
  open. Flapping at the threshold produces one event, not hundreds of rows.
- `PeakValue` tracks the extreme seen since the event opened; `LastValue` tracks the most
  recent sample.
- Kinds whose threshold is 0 are disabled and never evaluated.

`Sweep` runs every 5 seconds: any node whose `last_seen` is older than `NodeOfflineAfter`
opens `node_silent`, and its return closes the event. When `LocalNodeID` is set, local GPU
metrics older than `CollectStaleAfter` open `collector_stalled`, which is the same condition
`/api/v1/healthz` already reports.

`Restore` reads events left open by a previous process into memory at startup, so a restart
neither duplicates an open event nor strands one open forever.

### Schema, migration 006

```sql
CREATE TABLE IF NOT EXISTS alert_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    TEXT    NOT NULL,
    gpu_id     INTEGER,
    kind       TEXT    NOT NULL,
    threshold  REAL    NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,
    peak_value REAL    NOT NULL,
    last_value REAL    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_events_started ON alert_events(started_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_alert_open
    ON alert_events(node_id, COALESCE(gpu_id, -1), kind) WHERE ended_at IS NULL;
```

The partial unique index keeps "one open event per key" true in the database rather than only
in the evaluator's memory. Retention prunes closed events older than `--retention-alerts`
(default 90 days) and never touches open ones.

### Integration

| Mode | Who calls `Observe` | Who calls `Sweep` | `LocalNodeID` |
|------|--------------------|-------------------|----------------|
| standalone | collector GPU tick | main, 5s ticker | `local` |
| hub | ingest handler | main, 5s ticker | empty |
| agent | nobody: agents push raw samples, the hub evaluates | n/a | n/a |

The collector gains `SetAlerts(engine)` alongside the existing `SetVLLM`, which keeps the
constructor from growing a fourth positional argument. `api.Server` loses `checkAlerts`,
`activeAlerts`, `alertsMu` and its local `Alert` type, and gains a reference to the engine.

### State broadcast

`alerts` imports `collector` for the metric type, so `collector` cannot import `alerts`. The
state snapshot is therefore assembled and broadcast by `api`, which already imports both.

```json
{"type":"state","ts":1789600000,"nodes":[...],"devices":[...],"alerts":[...]}
```

A goroutine in `api` sends it on engine change and every 15 seconds regardless, so a tab that
connects between changes does not wait for the next one. Changes are debounced to at most one
snapshot per second. `BroadcastSink` keeps carrying metric snapshots unchanged.

### Freshness window

`storage.Open` accepts the collection interval and derives one window,
`max(5 * collectInterval, 30s)`, replacing the five hardcoded `now - 30` expressions.
`GetNodes` takes its offline threshold from configuration instead of the hardcoded 60
seconds, so the online dot and the `node_silent` event agree by construction.

`GetAllGPUProcesses` stops using a window at all: it selects rows at `MAX(ts)` per node and
GPU, the way the per-GPU endpoint already does, so exited processes leave the list at the
next collection tick.

### API

| Route | Change |
|-------|--------|
| `/api/v1/alerts` | Same shape; `alerts` now carries open events with `id`, `kind`, `started_at`, `peak_value` |
| `/api/v1/alerts/history` | New: `?from=&to=&node=&kind=&limit=` (default 200, newest first) |
| `/api/v1/status` | `alerts` carries the same open events |
| `/api/v1/ws` | New `state` message type |

Breaking change for API consumers: the alert object field `metric` becomes `kind` and gains
`id`, `started_at`, `ended_at`, `peak_value`. This is documented in README.

### Configuration

| Flag | Env | Default | Meaning |
|------|-----|---------|---------|
| `--alert-for` | `CUDASCOPE_ALERT_FOR` | 30s | Dwell before an event opens |
| `--alert-clear` | `CUDASCOPE_ALERT_CLEAR` | 60s | Dwell before an event closes |
| `--node-offline-after` | `CUDASCOPE_NODE_OFFLINE_AFTER` | 60s | Silence that marks a node offline and opens `node_silent` |
| `--retention-alerts` | `CUDASCOPE_RETENTION_ALERTS` | 2160h | Closed event retention (90 days) |

`--collect-stale-after` keeps its meaning and now also drives `collector_stalled`.

### UI

`metrics.ts` handles the `state` message and updates the `nodes`, `devices` and `alerts`
stores; `fetchStatus()` remains the initial load. A new `/alerts` page lists the journal with
open events first, filters by node and kind, and shows duration and peak value per row. The
navbar badge links to it. The red border on a GPU card keeps reading the same store.

### Error handling

A failed write leaves the evaluator's in-memory state intact, marks the candidate as needing
persistence and retries on the next observation, so a database hiccup costs journal rows and
never costs a live alert. `Sweep` reads freshness with one query and holds no write lock.
A duplicate-open violation of the partial index is logged and the existing row adopted.

## Testing

Tests come first, in the style already used here: names that read as the invariant.

**`internal/alerts`** (fake clock, fake store, no database)
- opens only after the dwell has elapsed
- a brief spike opens nothing
- closes only after normality has held
- a breach during the clear timer keeps one event open
- peak survives a later lower sample
- restore adopts an event left open by a previous run
- a silent node opens an event and its return closes it
- a zero threshold disables its kind

**`internal/storage`**
- the partial index rejects a second open event for one key
- retention removes closed events past the cutoff and keeps open ones
- `GetAllGPUProcesses` returns only the newest snapshot per GPU

**`internal/api`**
- a state snapshot is broadcast on change and on the ticker
- `/api/v1/alerts/history` honours range, node and limit

**`internal/collector`**
- the GPU tick calls `Observe` with the samples it stored

## Estimated size

Roughly 700 to 900 lines including tests, one migration, one new UI page.
