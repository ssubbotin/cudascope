package collector

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeOllama answers /api/ps and /api/version the way an ollama server does.
type fakeOllama struct {
	srv          *httptest.Server
	ps           atomic.Value // string, the /api/ps body
	version      atomic.Value // string, "" makes /api/version fail
	versionCalls atomic.Int64
}

func newFakeOllama(t *testing.T, ps, version string) *fakeOllama {
	t.Helper()
	f := &fakeOllama{}
	f.ps.Store(ps)
	f.version.Store(version)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, f.ps.Load().(string))
	})
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		f.versionCalls.Add(1)
		v := f.version.Load().(string)
		if v == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"version":%q}`, v)
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func psBody(name string, size, vram int64, expiresAt time.Time) string {
	return fmt.Sprintf(`{"models":[{"name":%q,"model":%q,"size":%d,"size_vram":%d,"context_length":4096,"expires_at":%q}]}`,
		name, name, size, vram, expiresAt.Format(time.RFC3339Nano))
}

// The reading says which model is loaded and how much of it is on the card.
func TestOllamaReportsWhatIsLoaded(t *testing.T) {
	expires := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	f := newFakeOllama(t, psBody("qwen3:8b", 8_000_000_000, 6_000_000_000, expires), "0.13.0")

	c := NewOllamaCollector(f.srv.URL, "local")
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if len(m.Models) != 1 {
		t.Fatalf("want one model, got %d", len(m.Models))
	}
	got := m.Models[0]
	if got.Name != "qwen3:8b" {
		t.Errorf("name = %q", got.Name)
	}
	if got.SizeBytes != 8_000_000_000 || got.VRAMBytes != 6_000_000_000 {
		t.Errorf("size/vram = %d/%d", got.SizeBytes, got.VRAMBytes)
	}
	if got.ExpiresAt != expires.Unix() {
		t.Errorf("expires at %d, want %d", got.ExpiresAt, expires.Unix())
	}
	if m.Version != "0.13.0" {
		t.Errorf("version = %q", m.Version)
	}
}

// A model that did not fit reports the share that did. This is the reading
// the GPU metrics cannot give: the card looks busy either way.
func TestOllamaReportsAModelThatDidNotFit(t *testing.T) {
	f := newFakeOllama(t, psBody("gemma4:31b", 20_000_000_000, 5_000_000_000, time.Now()), "0.13.0")

	c := NewOllamaCollector(f.srv.URL, "local")
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if share := m.Models[0].OnGPU(); share < 0.24 || share > 0.26 {
		t.Fatalf("on GPU share = %v, want about a quarter", share)
	}
}

// An idle server is a reading, not a failure: nothing is loaded, and the
// next request will pay for a load.
func TestOllamaIdleIsAReading(t *testing.T) {
	f := newFakeOllama(t, `{"models":[]}`, "0.13.0")

	c := NewOllamaCollector(f.srv.URL, "local")
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(m.Models) != 0 {
		t.Fatalf("want nothing loaded, got %d models", len(m.Models))
	}
	if m.Timestamp == 0 {
		t.Error("a reading with no models still has a timestamp")
	}
}

// The version is a label. It is asked for once and then left alone, so a
// scrape every few seconds does not turn into two requests every time.
func TestOllamaAsksForTheVersionOnce(t *testing.T) {
	f := newFakeOllama(t, `{"models":[]}`, "0.13.0")

	c := NewOllamaCollector(f.srv.URL, "local")
	for range 5 {
		if _, err := c.Collect(); err != nil {
			t.Fatalf("collect: %v", err)
		}
	}

	if got := f.versionCalls.Load(); got != 1 {
		t.Fatalf("asked for the version %d times, want once", got)
	}
}

// A server that will not say its version still reports what it is holding,
// and the question is not asked again until the cooldown has passed.
func TestOllamaSurvivesAnUnknownVersion(t *testing.T) {
	f := newFakeOllama(t, `{"models":[]}`, "")

	c := NewOllamaCollector(f.srv.URL, "local")
	for range 3 {
		m, err := c.Collect()
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if m.Version != "" {
			t.Fatalf("version = %q, want it left empty", m.Version)
		}
	}

	if got := f.versionCalls.Load(); got != 1 {
		t.Fatalf("retried the version %d times inside the cooldown", got)
	}
}

// A server that is not there is an error, so the caller can log it once and
// carry on rather than storing a reading that says nothing is loaded.
func TestOllamaUnreachableIsAnError(t *testing.T) {
	c := NewOllamaCollector("http://127.0.0.1:1", "local")
	if _, err := c.Collect(); err == nil {
		t.Fatal("want an error from an unreachable server")
	}
}
