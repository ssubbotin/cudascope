-- Raw metrics are keyed by whole seconds, so two rows with the same
-- timestamp, node and device are the same sample stored twice. That became
-- reachable once agents started buffering and resending: a POST can be
-- stored and still report failure, and the retry arrives as a duplicate.

DELETE FROM gpu_metrics_raw WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM gpu_metrics_raw GROUP BY ts, COALESCE(node_id, 'local'), gpu_id
);
DELETE FROM host_metrics_raw WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM host_metrics_raw GROUP BY ts, COALESCE(node_id, 'local')
);
DELETE FROM vllm_metrics_raw WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM vllm_metrics_raw GROUP BY ts, node_id
);
DELETE FROM gpu_processes WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM gpu_processes GROUP BY ts, COALESCE(node_id, 'local'), gpu_id, pid
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_raw ON gpu_metrics_raw(ts, COALESCE(node_id, 'local'), gpu_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_host_raw ON host_metrics_raw(ts, COALESCE(node_id, 'local'));
CREATE UNIQUE INDEX IF NOT EXISTS uq_vllm_raw ON vllm_metrics_raw(ts, node_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_proc ON gpu_processes(ts, COALESCE(node_id, 'local'), gpu_id, pid);

-- OR IGNORE so the fold can be re-run against a database that already has it.
INSERT OR IGNORE INTO schema_version (version) VALUES (8);
