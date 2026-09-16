-- The throttle mask in the rollups, so "did this card hold itself back last
-- night" can be answered past the day the raw rows live for.
ALTER TABLE gpu_metrics_1m ADD COLUMN throttle_reasons INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gpu_metrics_1h ADD COLUMN throttle_reasons INTEGER NOT NULL DEFAULT 0;

INSERT OR IGNORE INTO schema_version (version) VALUES (10);
