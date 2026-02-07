-- GPU device info
CREATE TABLE IF NOT EXISTS gpu_devices (
    id          INTEGER PRIMARY KEY,
    uuid        TEXT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    mem_total   INTEGER NOT NULL,
    driver_ver  TEXT,
    first_seen  INTEGER NOT NULL,
    node_id     TEXT DEFAULT 'local'
);

-- Raw GPU metrics (1-second resolution)
CREATE TABLE IF NOT EXISTS gpu_metrics_raw (
    ts              INTEGER NOT NULL,
    gpu_id          INTEGER NOT NULL,
    gpu_util        REAL,
    mem_util        REAL,
    mem_used        INTEGER,
    temperature     INTEGER,
    fan_speed       INTEGER,
    power_draw      REAL,
    power_limit     REAL,
    clock_gfx       INTEGER,
    clock_mem       INTEGER,
    pcie_tx         INTEGER,
    pcie_rx         INTEGER,
    pstate          INTEGER,
    encoder_util    REAL,
    decoder_util    REAL
);
CREATE INDEX IF NOT EXISTS idx_gpu_raw_ts ON gpu_metrics_raw(ts, gpu_id);

-- 1-minute rolled up GPU metrics
CREATE TABLE IF NOT EXISTS gpu_metrics_1m (
    ts              INTEGER NOT NULL,
    gpu_id          INTEGER NOT NULL,
    gpu_util_avg    REAL,
    gpu_util_max    REAL,
    mem_util_avg    REAL,
    mem_used_avg    INTEGER,
    mem_used_max    INTEGER,
    temperature_avg REAL,
    temperature_max INTEGER,
    fan_speed_avg   REAL,
    power_draw_avg  REAL,
    power_draw_max  REAL,
    clock_gfx_avg   REAL,
    clock_mem_avg   REAL,
    pcie_tx_avg     INTEGER,
    pcie_rx_avg     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_gpu_1m_ts ON gpu_metrics_1m(ts, gpu_id);

-- 1-hour rolled up GPU metrics
CREATE TABLE IF NOT EXISTS gpu_metrics_1h (
    ts              INTEGER NOT NULL,
    gpu_id          INTEGER NOT NULL,
    gpu_util_avg    REAL,
    gpu_util_max    REAL,
    mem_util_avg    REAL,
    mem_used_avg    INTEGER,
    mem_used_max    INTEGER,
    temperature_avg REAL,
    temperature_max INTEGER,
    power_draw_avg  REAL,
    power_draw_max  REAL
);
CREATE INDEX IF NOT EXISTS idx_gpu_1h_ts ON gpu_metrics_1h(ts, gpu_id);

-- Raw host metrics
CREATE TABLE IF NOT EXISTS host_metrics_raw (
    ts          INTEGER NOT NULL,
    node_id     TEXT DEFAULT 'local',
    cpu_percent REAL,
    mem_used    INTEGER,
    mem_total   INTEGER,
    disk_used   INTEGER,
    disk_total  INTEGER,
    net_rx      INTEGER,
    net_tx      INTEGER,
    load_1m     REAL,
    load_5m     REAL,
    load_15m    REAL
);
CREATE INDEX IF NOT EXISTS idx_host_raw_ts ON host_metrics_raw(ts, node_id);

-- GPU processes
CREATE TABLE IF NOT EXISTS gpu_processes (
    ts          INTEGER NOT NULL,
    gpu_id      INTEGER NOT NULL,
    pid         INTEGER NOT NULL,
    name        TEXT,
    gpu_mem     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_gpu_proc_ts ON gpu_processes(ts, gpu_id);

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);
INSERT OR IGNORE INTO schema_version (version) VALUES (1);
