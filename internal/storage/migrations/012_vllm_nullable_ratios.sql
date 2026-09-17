-- Three vLLM columns are ratios over what happened between two scrapes, and
-- a window where the denominator did not move has no value to report. They
-- were declared NOT NULL DEFAULT 0, so "no request finished" was stored as a
-- reading of zero: the chart fell to the floor for the whole of a long
-- generation, and AVG in the rollups counted those zeros as measurements.
--
-- SQLite cannot drop a NOT NULL, so the table is rebuilt. Existing rows are
-- carried over as they are: a zero already written cannot be told apart from
-- a window that genuinely measured zero, and guessing would rewrite history.
CREATE TABLE vllm_metrics_raw_new (
    ts INTEGER NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    requests_running INTEGER NOT NULL DEFAULT 0,
    requests_waiting INTEGER NOT NULL DEFAULT 0,
    kv_cache_usage REAL NOT NULL DEFAULT 0,
    generation_tokens_total INTEGER NOT NULL DEFAULT 0,
    prompt_tokens_total INTEGER NOT NULL DEFAULT 0,
    ttft_avg REAL,
    tpot_avg REAL,
    token_throughput REAL NOT NULL DEFAULT 0,
    prefix_cache_hit_rate REAL,
    num_preemptions INTEGER NOT NULL DEFAULT 0
);

INSERT INTO vllm_metrics_raw_new
    (ts, node_id, model_name, requests_running, requests_waiting, kv_cache_usage,
     generation_tokens_total, prompt_tokens_total, ttft_avg, tpot_avg,
     token_throughput, prefix_cache_hit_rate, num_preemptions)
SELECT ts, node_id, model_name, requests_running, requests_waiting, kv_cache_usage,
       generation_tokens_total, prompt_tokens_total, ttft_avg, tpot_avg,
       token_throughput, prefix_cache_hit_rate, num_preemptions
FROM vllm_metrics_raw;

DROP TABLE vllm_metrics_raw;
ALTER TABLE vllm_metrics_raw_new RENAME TO vllm_metrics_raw;

-- Dropping the table took its indexes with it.
CREATE INDEX IF NOT EXISTS idx_vllm_raw_ts ON vllm_metrics_raw(ts);
CREATE INDEX IF NOT EXISTS idx_vllm_raw_node ON vllm_metrics_raw(node_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS uq_vllm_raw ON vllm_metrics_raw(ts, node_id);

INSERT INTO schema_version (version) VALUES (12);
