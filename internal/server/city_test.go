package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/model"
)

// TestCity exercises /api/city against the unfold module itself: the
// packages come back sorted, the functions inside them sorted, and the
// measurements are the ones the map draws from.
func TestCity(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	srv := New(idx)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var view model.CityView
	getJSON(t, ts.URL+"/api/city", http.StatusOK, &view)
	if view.Module != "github.com/MaxInertia/unfold" {
		t.Errorf("module: got %q", view.Module)
	}
	if len(view.Packages) < 5 {
		t.Fatalf("expected the module's packages, got %d", len(view.Packages))
	}

	var server *model.CityPackage
	for i := range view.Packages {
		p := &view.Packages[i]
		if i > 0 && view.Packages[i-1].Path >= p.Path {
			t.Errorf("packages not sorted by path: %q before %q", view.Packages[i-1].Path, p.Path)
		}
		if strings.HasSuffix(p.Path, "/internal/server") {
			server = p
		}
		for j := 1; j < len(p.Funcs); j++ {
			if p.Funcs[j-1].Name > p.Funcs[j].Name {
				t.Errorf("%s: functions not sorted by name: %q before %q", p.Path, p.Funcs[j-1].Name, p.Funcs[j].Name)
			}
		}
	}
	if server == nil {
		t.Fatal("internal/server missing from the city")
	}
	if server.Name != "server" {
		t.Errorf("package name: got %q", server.Name)
	}

	// writeJSON is called by nearly every handler and calls nothing of ours;
	// handleHealth is reached only through the mux, so it has no caller in
	// the call graph but calls writeJSON.
	byName := map[string]model.CityFunc{}
	for _, f := range server.Funcs {
		byName[f.Name] = f
	}
	wj, ok := byName["writeJSON"]
	if !ok {
		t.Fatal("writeJSON missing")
	}
	if wj.Callers < 5 {
		t.Errorf("writeJSON callers: got %d, want many", wj.Callers)
	}
	if wj.Callees != 0 {
		t.Errorf("writeJSON callees: got %d, want 0 (it only calls the stdlib)", wj.Callees)
	}
	if wj.Lines < 3 || !strings.HasSuffix(wj.File, "/internal/server/server.go") {
		t.Errorf("writeJSON lines/file: got %d %q", wj.Lines, wj.File)
	}
	hh, ok := byName["Server.handleHealth"]
	if !ok {
		t.Fatal("Server.handleHealth missing")
	}
	if hh.Callers != 0 {
		t.Errorf("handleHealth callers: got %d, want 0 (registered, never called)", hh.Callers)
	}
	if hh.Callees < 1 {
		t.Errorf("handleHealth callees: got %d, want at least writeJSON", hh.Callees)
	}
	if hh.Changed {
		t.Error("changed must be unset outside diff mode")
	}

	// Memoized: the engine hands back the same view for the same index.
	v1, _ := idx.City()
	v2, _ := idx.City()
	if v1 != v2 {
		t.Error("City() is not memoized per index build")
	}

	t.Run("unknown-repo", func(t *testing.T) {
		// A single repo isn't a workspace, so a repo name has nothing to
		// resolve against.
		res, err := http.Get(ts.URL + "/api/city?repo=other")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Errorf("status: got %d want 501", res.StatusCode)
		}
	})
}
