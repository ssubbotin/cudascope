-- vLLM rollups. The raw table was neither rolled up nor pruned, so it grew
-- without bound; pruning it alone would have cut a year of inference history
-- down to a day. Same three tiers as GPU and host metrics.

CREATE TABLE IF NOT EXISTS vllm_metrics_1m (
    ts                      INTEGER NOT NULL,
    node_id                 TEXT NOT NULL DEFAULT '',
    model_name              TEXT NOT NULL DEFAULT '',
    requests_running_avg    REAL,
    requests_running_max    REAL,
    requests_waiting_avg    REAL,
    requests_waiting_max    REAL,
    kv_cache_usage_avg      REAL,
    kv_cache_usage_max      REAL,
    token_throughput_avg    REAL,
    token_throughput_max    REAL,
    ttft_avg                REAL,
    ttft_max                REAL,
    tpot_avg                REAL,
    tpot_max                REAL,
    prefix_cache_hit_rate_avg REAL,
    generation_tokens_total INTEGER,
    prompt_tokens_total     INTEGER,
    num_preemptions         INTEGER
);
CREATE INDEX IF NOT EXISTS idx_vllm_1m_ts ON vllm_metrics_1m(ts, node_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_vllm_1m ON vllm_metrics_1m(ts, node_id);

CREATE TABLE IF NOT EXISTS vllm_metrics_1h (
    ts                      INTEGER NOT NULL,
    node_id                 TEXT NOT NULL DEFAULT '',
    model_name              TEXT NOT NULL DEFAULT '',
    requests_running_avg    REAL,
    requests_running_max    REAL,
    requests_waiting_avg    REAL,
    requests_waiting_max    REAL,
    kv_cache_usage_avg      REAL,
    kv_cache_usage_max      REAL,
    token_throughput_avg    REAL,
    token_throughput_max    REAL,
    ttft_avg                REAL,
    ttft_max                REAL,
    tpot_avg                REAL,
    tpot_max                REAL,
    prefix_cache_hit_rate_avg REAL,
    generation_tokens_total INTEGER,
    prompt_tokens_total     INTEGER,
    num_preemptions         INTEGER
);
CREATE INDEX IF NOT EXISTS idx_vllm_1h_ts ON vllm_metrics_1h(ts, node_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_vllm_1h ON vllm_metrics_1h(ts, node_id);

-- The raw table had no index on node_id, which every query filters by.
CREATE INDEX IF NOT EXISTS idx_vllm_raw_node ON vllm_metrics_raw(node_id, ts);

INSERT INTO schema_version (version) VALUES (7);
