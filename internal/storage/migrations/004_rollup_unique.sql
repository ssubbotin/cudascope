-- Remove duplicate rollup rows (keep one per bucket)
DELETE FROM gpu_metrics_1m WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM gpu_metrics_1m GROUP BY ts, node_id, gpu_id
);
DELETE FROM gpu_metrics_1h WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM gpu_metrics_1h GROUP BY ts, node_id, gpu_id
);
DELETE FROM host_metrics_1m WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM host_metrics_1m GROUP BY ts, node_id
);
DELETE FROM host_metrics_1h WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM host_metrics_1h GROUP BY ts, node_id
);

-- Add unique constraints to prevent future duplicates
CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_1m ON gpu_metrics_1m(ts, node_id, gpu_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_1h ON gpu_metrics_1h(ts, node_id, gpu_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_host_1m ON host_metrics_1m(ts, node_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_host_1h ON host_metrics_1h(ts, node_id);

INSERT INTO schema_version (version) VALUES (4);
