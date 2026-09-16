package collector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// vllmModelsRetry bounds how often the /v1/models fallback is tried while
// the model name is still unknown.
const vllmModelsRetry = time.Minute

// VLLMCollector scrapes metrics from a vLLM inference server.
type VLLMCollector struct {
	baseURL string
	nodeID  string
	client  *http.Client

	// previous counter values for rate computation
	prevGenTokens    int64
	prevGenTokensTs  time.Time
	prevTTFTSum      float64
	prevTTFTCount    float64
	prevTPOTSum      float64
	prevTPOTCount    float64
	prevCacheHits    float64
	prevCacheQueries float64

	modelName string

	// modelsRetryAt throttles the /v1/models fallback.
	modelsRetryAt time.Time
}

// NewVLLMCollector creates a collector that scrapes vLLM's /metrics endpoint.
func NewVLLMCollector(baseURL, nodeID string) *VLLMCollector {
	// Trim trailing slash
	baseURL = strings.TrimRight(baseURL, "/")

	// No request is made here: construction happens before the HTTP server
	// is up, and the first scrape discovers the model name anyway.
	return &VLLMCollector{
		baseURL: baseURL,
		nodeID:  nodeID,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// fetchModelName queries /v1/models to discover the served model name. It
// returns "" when the name cannot be determined.
func (v *VLLMCollector) fetchModelName() string {
	resp, err := v.client.Get(v.baseURL + "/v1/models")
	if err != nil {
		log.Printf("vllm: failed to fetch model name: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("vllm: /v1/models returned status %d", resp.StatusCode)
		return ""
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("vllm: failed to decode /v1/models: %v", err)
		return ""
	}

	if len(result.Data) == 0 {
		return ""
	}
	return result.Data[0].ID
}

// resolveModelName keeps modelName current. The label is re-read on every
// scrape so that a vLLM service swap is picked up, but a scrape that carries
// no label leaves the last known name alone: /metrics answers without it
// while the engine is still starting, and blanking the field would store
// empty model names for those samples.
//
// A missing label also sends us to /v1/models, at most once per
// vllmModelsRetry. That covers both the name never having been known and a
// swap to a model whose /metrics carries no label, which the label alone can
// never reveal. The cooldown matters because this is a blocking call on the
// collector's own goroutine, sharing the client timeout with the scrape.
func (v *VLLMCollector) resolveModelName(scraped string) {
	if scraped != "" {
		if scraped != v.modelName {
			log.Printf("vllm: model name = %s", scraped)
			v.modelName = scraped
		}
		return
	}

	if time.Now().Before(v.modelsRetryAt) {
		return
	}
	v.modelsRetryAt = time.Now().Add(vllmModelsRetry)

	if name := v.fetchModelName(); name != "" && name != v.modelName {
		log.Printf("vllm: model name = %s", name)
		v.modelName = name
	}
}

// Collect scrapes the vLLM /metrics endpoint and returns parsed metrics.
func (v *VLLMCollector) Collect() (*VLLMMetrics, error) {
	resp, err := v.client.Get(v.baseURL + "/metrics")
	if err != nil {
		return nil, fmt.Errorf("GET /metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /metrics: status %d", resp.StatusCode)
	}

	raw, scraped, err := v.parsePrometheusMetrics(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}

	v.resolveModelName(scraped)

	now := time.Now()
	m := &VLLMMetrics{
		NodeID:                v.nodeID,
		Timestamp:             now.Unix(),
		ModelName:             v.modelName,
		RequestsRunning:       int(raw["vllm:num_requests_running"]),
		RequestsWaiting:       int(raw["vllm:num_requests_waiting"]),
		KVCacheUsage:          raw["vllm:kv_cache_usage_perc"],
		GenerationTokensTotal: int64(raw["vllm:generation_tokens_total"]),
		PromptTokensTotal:     int64(raw["vllm:prompt_tokens_total"]),
		NumPreemptions:        int64(raw["vllm:num_preemptions_total"]),
	}

	// Compute TTFT average (delta of sum / delta of count)
	ttftSum := raw["vllm:time_to_first_token_seconds_sum"]
	ttftCount := raw["vllm:time_to_first_token_seconds_count"]
	if ttftCount > v.prevTTFTCount {
		deltaSum := ttftSum - v.prevTTFTSum
		deltaCount := ttftCount - v.prevTTFTCount
		m.TimeToFirstTokenAvg = deltaSum / deltaCount
	}
	v.prevTTFTSum = ttftSum
	v.prevTTFTCount = ttftCount

	// Compute TPOT average (delta of sum / delta of count)
	tpotSum := raw["vllm:time_per_output_token_seconds_sum"]
	tpotCount := raw["vllm:time_per_output_token_seconds_count"]
	if tpotCount > v.prevTPOTCount {
		deltaSum := tpotSum - v.prevTPOTSum
		deltaCount := tpotCount - v.prevTPOTCount
		m.TimePerOutputTokenAvg = deltaSum / deltaCount
	}
	v.prevTPOTSum = tpotSum
	v.prevTPOTCount = tpotCount

	// Compute prefix cache hit rate (delta hits / delta queries)
	cacheHits := raw["vllm:prefix_cache_hits_total"]
	cacheQueries := raw["vllm:prefix_cache_queries_total"]
	if cacheQueries > v.prevCacheQueries {
		deltaHits := cacheHits - v.prevCacheHits
		deltaQueries := cacheQueries - v.prevCacheQueries
		m.PrefixCacheHitRate = deltaHits / deltaQueries
	}
	v.prevCacheHits = cacheHits
	v.prevCacheQueries = cacheQueries

	// Compute token throughput (delta generation tokens / elapsed seconds)
	genTokens := int64(raw["vllm:generation_tokens_total"])
	if !v.prevGenTokensTs.IsZero() && genTokens > v.prevGenTokens {
		elapsed := now.Sub(v.prevGenTokensTs).Seconds()
		if elapsed > 0 {
			m.TokenThroughput = float64(genTokens-v.prevGenTokens) / elapsed
		}
	}
	v.prevGenTokens = genTokens
	v.prevGenTokensTs = now

	return m, nil
}

// parsePrometheusMetrics does a simple line-by-line parse of Prometheus text format.
// It returns a map of metric name -> value (last value wins for gauge-like metrics).
func (v *VLLMCollector) parsePrometheusMetrics(r io.Reader) (map[string]float64, string, error) {
	var modelName string
	result := make(map[string]float64)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if line == "" || line[0] == '#' {
			continue
		}

		// Extract metric name (before '{' or ' ')
		name, value, ok := parsePromLine(line)
		if !ok {
			continue
		}

		// Only collect metrics we care about
		if !strings.HasPrefix(name, "vllm:") {
			continue
		}

		result[name] = value

		// Extract model name from labels — update every time so a vLLM
		// service swap (e.g. stop qwen-coder, start gemma) is reflected.
		if idx := strings.Index(line, "model_name=\""); idx >= 0 {
			start := idx + len("model_name=\"")
			end := strings.Index(line[start:], "\"")
			if end > 0 {
				modelName = line[start : start+end]
			}
		}
	}

	return result, modelName, scanner.Err()
}

// parsePromLine parses a single Prometheus exposition line into metric name and value.
// Handles both "metric_name value" and "metric_name{labels} value" formats.
func parsePromLine(line string) (name string, value float64, ok bool) {
	// Find the metric name (everything before '{' or first space)
	nameEnd := len(line)
	for i, c := range line {
		if c == '{' || c == ' ' || c == '\t' {
			nameEnd = i
			break
		}
	}
	name = line[:nameEnd]
	if name == "" {
		return "", 0, false
	}

	// Find the value (last space-separated token)
	lastSpace := strings.LastIndexByte(line, ' ')
	if lastSpace < 0 {
		lastSpace = strings.LastIndexByte(line, '\t')
	}
	if lastSpace < 0 {
		return "", 0, false
	}

	valStr := strings.TrimSpace(line[lastSpace+1:])
	// Strip optional timestamp (second space-separated number after value)
	if spIdx := strings.IndexByte(valStr, ' '); spIdx >= 0 {
		valStr = valStr[:spIdx]
	}

	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return "", 0, false
	}

	return name, val, true
}
