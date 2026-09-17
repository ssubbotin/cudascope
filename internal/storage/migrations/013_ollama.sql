-- What an ollama server holds in memory, one row per model per collection
-- tick. Ollama publishes no counters, so there is nothing here to average
-- or to take a rate of: the row is the state of that moment.
--
-- Shaped after gpu_processes rather than after the metric tables. Both are
-- a list that changes when something is loaded or unloaded, both are read
-- by taking the newest tick, and both are pruned with the raw samples.
CREATE TABLE IF NOT EXISTS ollama_models_raw (
    ts             INTEGER NOT NULL,
    node_id        TEXT    NOT NULL DEFAULT 'local',
    model          TEXT    NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    vram_bytes     INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 0,
    expires_at     INTEGER,
    version        TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ollama_models_ts ON ollama_models_raw(ts);
CREATE INDEX IF NOT EXISTS idx_ollama_models_node ON ollama_models_raw(node_id, ts);

-- One row per tick, node and model, so a resent batch overwrites rather
-- than duplicates.
CREATE UNIQUE INDEX IF NOT EXISTS uq_ollama_models
    ON ollama_models_raw(ts, node_id, model);

-- A tick during which nothing was loaded is a reading too, and it has no
-- model row to carry it. Without this table an idle server would be
-- indistinguishable from one that stopped answering.
CREATE TABLE IF NOT EXISTS ollama_ticks (
    ts      INTEGER NOT NULL,
    node_id TEXT    NOT NULL DEFAULT 'local',
    loaded  INTEGER NOT NULL DEFAULT 0,
    version TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_ollama_ticks ON ollama_ticks(ts, node_id);
CREATE INDEX IF NOT EXISTS idx_ollama_ticks_node ON ollama_ticks(node_id, ts);

INSERT INTO schema_version (version) VALUES (13);
