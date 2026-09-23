package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sergey/cudascope/internal/buildinfo"
	"github.com/sergey/cudascope/internal/storage"
)

func TestStatusSaysWhichBuildIsServing(t *testing.T) {
	db, err := storage.Open(t.TempDir(), storage.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	build := buildinfo.Info{Version: "v0.1.0", Revision: "683f145", BuiltAt: "2026-09-23T09:43:11Z"}
	s := NewServer(Options{Store: db, Hub: NewHub(), Build: build})

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/status", nil))

	var body struct {
		Build *buildinfo.Info `json:"build"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Build == nil || *body.Build != build {
		t.Fatalf("got build %+v, want %+v", body.Build, build)
	}
}
