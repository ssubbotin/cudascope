CREATE TABLE IF NOT EXISTS vllm_metrics_raw (
    ts INTEGER NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    requests_running INTEGER NOT NULL DEFAULT 0,
    requests_waiting INTEGER NOT NULL DEFAULT 0,
    kv_cache_usage REAL NOT NULL DEFAULT 0,
    generation_tokens_total INTEGER NOT NULL DEFAULT 0,
    prompt_tokens_total INTEGER NOT NULL DEFAULT 0,
    ttft_avg REAL NOT NULL DEFAULT 0,
    tpot_avg REAL NOT NULL DEFAULT 0,
    token_throughput REAL NOT NULL DEFAULT 0,
    prefix_cache_hit_rate REAL NOT NULL DEFAULT 0,
    num_preemptions INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_vllm_raw_ts ON vllm_metrics_raw(ts);

INSERT INTO schema_version (version) VALUES (5);
