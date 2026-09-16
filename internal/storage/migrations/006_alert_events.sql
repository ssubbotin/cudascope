-- Alert events: one row per alert from the moment it opened to the moment
-- it cleared. Before this, alerts existed only in the API server's memory
-- and only while somebody was looking at the dashboard.
CREATE TABLE IF NOT EXISTS alert_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    TEXT    NOT NULL,
    gpu_id     INTEGER,                     -- NULL for node-level kinds
    kind       TEXT    NOT NULL,
    threshold  REAL    NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,                     -- NULL while the event is open
    peak_value REAL    NOT NULL DEFAULT 0,
    last_value REAL    NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_alert_events_started ON alert_events(started_at);
CREATE INDEX IF NOT EXISTS idx_alert_events_node ON alert_events(node_id, started_at);

-- One open event per node, GPU and kind. COALESCE folds the NULL of a
-- node-level event into a real value, because NULL never equals NULL in an
-- index and those rows would otherwise be free to duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS uq_alert_open
    ON alert_events(node_id, COALESCE(gpu_id, -1), kind) WHERE ended_at IS NULL;

INSERT INTO schema_version (version) VALUES (6);
