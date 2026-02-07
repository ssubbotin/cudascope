-- 1-minute rolled up host metrics
CREATE TABLE IF NOT EXISTS host_metrics_1m (
    ts          INTEGER NOT NULL,
    node_id     TEXT DEFAULT 'local',
    cpu_percent_avg REAL,
    cpu_percent_max REAL,
    mem_used_avg    INTEGER,
    mem_used_max    INTEGER,
    mem_total       INTEGER,
    disk_used       INTEGER,
    disk_total      INTEGER,
    net_rx_avg      INTEGER,
    net_tx_avg      INTEGER,
    load_1m_avg     REAL,
    load_1m_max     REAL
);
CREATE INDEX IF NOT EXISTS idx_host_1m_ts ON host_metrics_1m(ts, node_id);

-- 1-hour rolled up host metrics
CREATE TABLE IF NOT EXISTS host_metrics_1h (
    ts          INTEGER NOT NULL,
    node_id     TEXT DEFAULT 'local',
    cpu_percent_avg REAL,
    cpu_percent_max REAL,
    mem_used_avg    INTEGER,
    mem_used_max    INTEGER,
    mem_total       INTEGER,
    load_1m_avg     REAL,
    load_1m_max     REAL
);
CREATE INDEX IF NOT EXISTS idx_host_1h_ts ON host_metrics_1h(ts, node_id);

UPDATE schema_version SET version = 2 WHERE version = 1;
