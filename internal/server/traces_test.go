package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/traces"
)

// The trace endpoints list without events and serve one with them; a name
// that is not a trace is a 404, and writing is not offered.
func TestTracesEndpoints(t *testing.T) {
	store := traces.NewStore(t.TempDir())
	srv := New(stubEngine{})
	srv.SetTraces(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var empty struct {
		Traces []traces.Summary `json:"traces"`
		Dir    string           `json:"dir"`
	}
	getJSON(t, ts.URL+"/api/traces", http.StatusOK, &empty)
	if empty.Traces == nil || len(empty.Traces) != 0 || !strings.HasSuffix(empty.Dir, filepath.Join(".unfold", "traces")) {
		t.Fatalf("empty list: %+v", empty)
	}

	if _, err := store.Save(traces.Trace{
		Name: "TestSum", Test: "TestSum", Package: "example.com/m/calc",
		Events: []traces.Event{
			{G: 1, Kind: "enter", Fn: "example.com/m/calc.TestSum", Line: 5},
			{G: 1, Kind: "line", Line: 6, T: 3},
			{G: 1, Kind: "exit", T: 9},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var list struct {
		Traces []traces.Summary `json:"traces"`
	}
	getJSON(t, ts.URL+"/api/traces", http.StatusOK, &list)
	if len(list.Traces) != 1 || list.Traces[0].Name != "TestSum" || list.Traces[0].Events != 3 {
		t.Fatalf("list: %+v", list)
	}
	// The listing is a summary: the raw body carries no events array.
	res, err := http.Get(ts.URL + "/api/traces")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	json.NewDecoder(res.Body).Decode(&raw)
	if _, ok := raw["traces"].([]any)[0].(map[string]any)["events"].(float64); !ok {
		t.Errorf("summary events should be a count: %v", raw)
	}

	var tr traces.Trace
	getJSON(t, ts.URL+"/api/traces/TestSum", http.StatusOK, &tr)
	if tr.Name != "TestSum" || len(tr.Events) != 3 || tr.Events[0].Fn != "example.com/m/calc.TestSum" {
		t.Fatalf("get: %+v", tr)
	}

	missing, _ := http.Get(ts.URL + "/api/traces/nope")
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("missing trace: %d", missing.StatusCode)
	}
	post, _ := http.Post(ts.URL+"/api/traces", "application/json", strings.NewReader("{}"))
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", post.StatusCode)
	}
}

// Without a store the endpoints say so rather than serving nothing.
func TestTracesDisabled(t *testing.T) {
	ts := httptest.NewServer(New(stubEngine{}).Handler())
	defer ts.Close()
	res, _ := http.Get(ts.URL + "/api/traces")
	if res.StatusCode != http.StatusNotImplemented {
		t.Errorf("status: %d", res.StatusCode)
	}
}
