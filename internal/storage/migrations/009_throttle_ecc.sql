-- Why a card slowed down, and whether its memory is throwing errors. The
-- first is the question asked every time a job runs slower than yesterday;
-- the second is how a card announces that it is dying.
ALTER TABLE gpu_metrics_raw ADD COLUMN throttle_reasons INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gpu_metrics_raw ADD COLUMN ecc_corrected INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gpu_metrics_raw ADD COLUMN ecc_uncorrected INTEGER NOT NULL DEFAULT 0;

-- What a card can answer at all, asked once when it is registered. Consumer
-- cards report no ECC, and their zeros must not be drawn as a clean bill of
-- health.
ALTER TABLE gpu_devices ADD COLUMN ecc_supported INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gpu_devices ADD COLUMN throttle_supported INTEGER NOT NULL DEFAULT 0;

INSERT OR IGNORE INTO schema_version (version) VALUES (9);
