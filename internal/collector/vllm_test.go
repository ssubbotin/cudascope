package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// vllmMetricsBody renders a minimal /metrics page. An empty model means the
// exposition carries no model_name label, which is what a vLLM that has just
// started (or one run with --disable-log-stats) looks like.
func vllmMetricsBody(model string) string {
	if model == "" {
		return "vllm:num_requests_running 2.0\nvllm:generation_tokens_total 100.0\n"
	}
	return fmt.Sprintf(
		"vllm:num_requests_running{model_name=%q} 2.0\nvllm:generation_tokens_total{model_name=%q} 100.0\n",
		model, model)
}

type fakeVLLM struct {
	srv          *httptest.Server
	body         atomic.Value // string, the /metrics page
	modelsCalls  atomic.Int64
	modelsAnswer atomic.Value // string, "" makes /v1/models fail
	allCalls     atomic.Int64
}

func newFakeVLLM(t *testing.T, metricsModel, modelsAnswer string) *fakeVLLM {
	t.Helper()
	f := &fakeVLLM{}
	f.body.Store(vllmMetricsBody(metricsModel))
	f.modelsAnswer.Store(modelsAnswer)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, f.body.Load().(string))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		f.modelsCalls.Add(1)
		answer := f.modelsAnswer.Load().(string)
		if answer == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": answer}},
		})
	})

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.allCalls.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func mustCollect(t *testing.T, v *VLLMCollector) *VLLMMetrics {
	t.Helper()
	m, err := v.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return m
}

// A scrape that carries no model_name label must not blank out the name we
// already know: during a vLLM restart /metrics answers before the label is
// there, and the panel would show an empty model for those samples.
func TestCollectKeepsLastKnownModelName(t *testing.T) {
	f := newFakeVLLM(t, "qwen3.8", "")
	v := NewVLLMCollector(f.srv.URL, "local")

	if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
		t.Fatalf("first scrape: got %q, want %q", got, "qwen3.8")
	}

	f.body.Store(vllmMetricsBody(""))
	if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
		t.Errorf("degraded scrape: got %q, want the last known %q", got, "qwen3.8")
	}
}

// A real model swap must still be picked up; that is what the label is
// re-read for on every scrape.
func TestCollectFollowsModelSwap(t *testing.T) {
	f := newFakeVLLM(t, "qwen3.8", "")
	v := NewVLLMCollector(f.srv.URL, "local")

	if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
		t.Fatalf("first scrape: got %q", got)
	}

	f.body.Store(vllmMetricsBody("gemma4"))
	if got := mustCollect(t, v).ModelName; got != "gemma4" {
		t.Errorf("after swap: got %q, want %q", got, "gemma4")
	}
}

// The /v1/models fallback is bounded by a cooldown: the call is blocking,
// shares the 10s client timeout with the scrape, and logs a line each time,
// so a scrape every 5s must not mean a models query every 5s.
func TestCollectQueriesModelsAtMostOncePerCooldown(t *testing.T) {
	f := newFakeVLLM(t, "", "qwen3.8")
	v := NewVLLMCollector(f.srv.URL, "local")

	for i := 0; i < 5; i++ {
		if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
			t.Fatalf("scrape %d: got %q, want %q", i, got, "qwen3.8")
		}
	}

	if got := f.modelsCalls.Load(); got != 1 {
		t.Errorf("/v1/models called %d times across 5 scrapes, want 1", got)
	}
}

// Construction must not talk to vLLM: it happens before the HTTP server is
// up, so an unreachable or slow endpoint delays the whole process by up to
// the client timeout for a value the first scrape fetches anyway.
func TestNewVLLMCollectorDoesNotCallTheServer(t *testing.T) {
	f := newFakeVLLM(t, "qwen3.8", "qwen3.8")
	NewVLLMCollector(f.srv.URL, "local")

	if got := f.allCalls.Load(); got != 0 {
		t.Errorf("constructor made %d request(s), want 0", got)
	}
}

// When neither source knows the name, the fallback must still be throttled:
// retrying a failing /v1/models on every 5s scrape adds a blocking call and
// a log line each time, on the goroutine that collects the metrics.
func TestCollectThrottlesTheModelsFallbackOnFailure(t *testing.T) {
	f := newFakeVLLM(t, "", "")
	v := NewVLLMCollector(f.srv.URL, "local")

	for i := 0; i < 5; i++ {
		if got := mustCollect(t, v).ModelName; got != "" {
			t.Fatalf("scrape %d: got %q, want an empty name", i, got)
		}
	}

	if got := f.modelsCalls.Load(); got != 1 {
		t.Errorf("/v1/models called %d times across 5 scrapes, want 1", got)
	}
}

// A swap to a model whose /metrics carries no model_name label cannot be
// seen in the label, so the fallback has to be consulted again rather than
// reporting the retired model for as long as the process runs.
func TestCollectFollowsAModelSwapWithoutALabel(t *testing.T) {
	f := newFakeVLLM(t, "", "qwen3.8")
	v := NewVLLMCollector(f.srv.URL, "local")

	if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
		t.Fatalf("first scrape: got %q, want %q", got, "qwen3.8")
	}

	// vLLM comes back on another model, still without the label.
	f.modelsAnswer.Store("gemma4")
	if got := mustCollect(t, v).ModelName; got != "qwen3.8" {
		t.Errorf("inside the cooldown: got %q, want the known %q", got, "qwen3.8")
	}

	v.modelsRetryAt = time.Now() // cooldown elapsed
	if got := mustCollect(t, v).ModelName; got != "gemma4" {
		t.Errorf("after the cooldown: got %q, want %q", got, "gemma4")
	}
}

// Every field read from the exposition is optional, and a missing key reads
// as zero. A scrape carrying no vLLM metric at all is therefore a series of
// convincing zeroes: an idle engine with an empty KV cache, stored with a
// fresh timestamp. The GPU collector already refuses that shape of sample.
func TestAScrapeWithoutVLLMMetricsIsRefused(t *testing.T) {
	f := newFakeVLLM(t, "qwen", "")
	f.body.Store("# HELP process_cpu_seconds_total Total user and system CPU time\nprocess_cpu_seconds_total 1.5\n")

	v := NewVLLMCollector(f.srv.URL, "local")
	m, err := v.Collect()
	if err == nil {
		t.Fatalf("a page with no vLLM metric produced a sample: %+v", m)
	}
	if m != nil {
		t.Fatalf("want no sample, got %+v", m)
	}
}

// vLLM renamed the cache gauge between engine versions. Reading only the
// newer name left the panel showing an empty KV cache for ever on the older
// one, which is indistinguishable from an idle server.
func TestCollectReadsEitherCacheMetricName(t *testing.T) {
	f := newFakeVLLM(t, "", "")
	f.body.Store("vllm:num_requests_running 1.0\nvllm:gpu_cache_usage_perc 0.42\n")

	v := NewVLLMCollector(f.srv.URL, "local")
	m := mustCollect(t, v)
	if m.KVCacheUsage != 0.42 {
		t.Fatalf("kv cache usage = %v, want 0.42", m.KVCacheUsage)
	}

	f.body.Store("vllm:num_requests_running 1.0\nvllm:kv_cache_usage_perc 0.77\n")
	m = mustCollect(t, v)
	if m.KVCacheUsage != 0.77 {
		t.Fatalf("kv cache usage = %v, want 0.77", m.KVCacheUsage)
	}
}
