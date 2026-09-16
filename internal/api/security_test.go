package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sergey/cudascope/internal/collector"
	"github.com/sergey/cudascope/internal/storage"
)

func newSecuredServer(t *testing.T, opts Options) (*Server, *storage.DB) {
	t.Helper()

	db, err := storage.Open(t.TempDir(), storage.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.RegisterNode("local", "testhost", 1); err != nil {
		t.Fatalf("register node: %v", err)
	}

	opts.Store = db
	opts.Hub = NewHub()
	return NewServer(opts), db
}

func ingestRequest(t *testing.T) *http.Request {
	t.Helper()
	payload, err := json.Marshal([]collector.GPUMetrics{{
		NodeID: "gpu-node-1", GPUID: 0, Temperature: 60, Timestamp: time.Now().Unix(),
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return httptest.NewRequest("POST", "/api/v1/ingest/gpu-metrics", bytes.NewReader(payload))
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(rec, r)
	return rec
}

// A hub accepts writes from anything that can reach the port. Before this,
// turning on --auth protected the dashboard and left the ingest routes wide
// open, so any host on the network could register a node and fill the
// database.
func TestIngestNeedsTheTokenWhenOneIsSet(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{IngestToken: "s3cret"})

	if code := serve(srv, ingestRequest(t)).Code; code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ingest = %d, want 401", code)
	}

	wrong := ingestRequest(t)
	wrong.Header.Set("Authorization", "Bearer nope")
	if code := serve(srv, wrong).Code; code != http.StatusUnauthorized {
		t.Fatalf("ingest with the wrong token = %d, want 401", code)
	}

	right := ingestRequest(t)
	right.Header.Set("Authorization", "Bearer s3cret")
	if code := serve(srv, right).Code; code != http.StatusOK {
		t.Fatalf("ingest with the right token = %d, want 200", code)
	}
}

// Asking for authentication and getting it on reads alone is the surprise
// worth removing: with --auth set and no token, ingest takes the same
// credentials.
func TestIngestAcceptsTheBasicCredentialsWhenNoTokenIsSet(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{Auth: "admin:hunter2"})

	if code := serve(srv, ingestRequest(t)).Code; code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ingest = %d, want 401", code)
	}

	authed := ingestRequest(t)
	authed.SetBasicAuth("admin", "hunter2")
	if code := serve(srv, authed).Code; code != http.StatusOK {
		t.Fatalf("ingest with the dashboard credentials = %d, want 200", code)
	}
}

func TestIngestStaysOpenWhenNothingIsConfigured(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{})

	if code := serve(srv, ingestRequest(t)).Code; code != http.StatusOK {
		t.Fatalf("ingest on an unconfigured hub = %d, want 200", code)
	}
}

// A health probe carries no credentials, so the check must stay reachable
// however the rest is protected.
func TestHealthzStaysReachable(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{Auth: "admin:hunter2", IngestToken: "s3cret"})

	rec := serve(srv, httptest.NewRequest("GET", "/api/v1/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
}

func TestReadsStillNeedTheirCredentials(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{Auth: "admin:hunter2"})

	if code := serve(srv, httptest.NewRequest("GET", "/api/v1/status", nil)).Code; code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", code)
	}

	authed := httptest.NewRequest("GET", "/api/v1/status", nil)
	authed.SetBasicAuth("admin", "hunter2")
	if code := serve(srv, authed).Code; code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", code)
	}
}

// The API answered every origin with a wildcard, so any page open in the
// browser could read a dashboard reachable from it.
func TestCrossOriginIsOffUntilAskedFor(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{})

	rec := serve(srv, httptest.NewRequest("GET", "/api/v1/status", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want it absent", got)
	}
}

func TestCrossOriginAllowsTheConfiguredOrigin(t *testing.T) {
	srv, _ := newSecuredServer(t, Options{CORSOrigin: "https://dash.example"})

	rec := serve(srv, httptest.NewRequest("GET", "/api/v1/status", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dash.example" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want Origin", got)
	}
}

// A quote in a label value ends the value early and makes the rest of the
// exposition unparseable, so one oddly named card breaks every metric a
// scraper reads from this endpoint.
func TestPrometheusEscapesLabelValues(t *testing.T) {
	srv, db := newSecuredServer(t, Options{})

	err := db.RegisterGPUDevices("local", []collector.GPUDevice{{
		ID: 0, UUID: "GPU-1", Name: `RTX "4090" \ lab`, MemTotal: 24564, DriverVer: "550",
	}})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	if err := db.WriteGPUMetrics([]collector.GPUMetrics{{
		NodeID: "local", GPUID: 0, Timestamp: time.Now().Unix(), GPUUtil: 50,
	}}); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	rec := serve(srv, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	want := `gpu_name="RTX \"4090\" \\ lab"`
	if !strings.Contains(body, want) {
		t.Fatalf("label value is not escaped, want a line containing %s, got:\n%s", want, body)
	}

	// Every exposition line must carry an even number of unescaped quotes.
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if quotes := countUnescapedQuotes(line); quotes%2 != 0 {
			t.Fatalf("line has an unterminated label value: %s", line)
		}
	}
}

func countUnescapedQuotes(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++ // skip whatever is escaped
		case '"':
			n++
		}
	}
	return n
}
