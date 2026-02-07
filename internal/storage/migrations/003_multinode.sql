-- Migration 003: Multi-node support

-- Nodes table for agent registration and heartbeat tracking
CREATE TABLE IF NOT EXISTS nodes (
    node_id     TEXT PRIMARY KEY,
    hostname    TEXT NOT NULL,
    gpu_count   INTEGER DEFAULT 0,
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL
);

-- Add node_id to GPU metric tables
ALTER TABLE gpu_metrics_raw ADD COLUMN node_id TEXT DEFAULT 'local';
ALTER TABLE gpu_metrics_1m ADD COLUMN node_id TEXT DEFAULT 'local';
ALTER TABLE gpu_metrics_1h ADD COLUMN node_id TEXT DEFAULT 'local';
ALTER TABLE gpu_processes ADD COLUMN node_id TEXT DEFAULT 'local';

-- Recreate gpu_devices with composite primary key (node_id, gpu_id)
CREATE TABLE IF NOT EXISTS gpu_devices_v2 (
    node_id     TEXT NOT NULL DEFAULT 'local',
    gpu_id      INTEGER NOT NULL,
    uuid        TEXT NOT NULL,
    name        TEXT NOT NULL,
    mem_total   INTEGER NOT NULL,
    driver_ver  TEXT,
    first_seen  INTEGER NOT NULL,
    PRIMARY KEY (node_id, gpu_id)
);
INSERT OR IGNORE INTO gpu_devices_v2 (node_id, gpu_id, uuid, name, mem_total, driver_ver, first_seen)
    SELECT COALESCE(node_id, 'local'), id, uuid, name, mem_total, driver_ver, first_seen FROM gpu_devices;
DROP TABLE IF EXISTS gpu_devices;
ALTER TABLE gpu_devices_v2 RENAME TO gpu_devices;

-- Register 'local' node for standalone mode
INSERT OR IGNORE INTO nodes (node_id, hostname, first_seen, last_seen)
    VALUES ('local', 'local', strftime('%s','now'), strftime('%s','now'));

-- Add node-aware indexes
CREATE INDEX IF NOT EXISTS idx_gpu_raw_node ON gpu_metrics_raw(node_id, ts, gpu_id);
CREATE INDEX IF NOT EXISTS idx_gpu_1m_node ON gpu_metrics_1m(node_id, ts, gpu_id);
CREATE INDEX IF NOT EXISTS idx_gpu_1h_node ON gpu_metrics_1h(node_id, ts, gpu_id);
CREATE INDEX IF NOT EXISTS idx_gpu_proc_node ON gpu_processes(node_id, ts, gpu_id);

UPDATE schema_version SET version = 3 WHERE version = 2;
