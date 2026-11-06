package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/difftour"
	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/tours"
)

// alteredBase is the unfold index posing as the diff base, with two edits
// faked on top: handleHealth's body differs, and handleDiffTour does not exist
// yet. Everything else matches, so exactly those two are "changed".
type alteredBase struct {
	*indexer.Indexer
	modified, missing model.TargetID
}

func (b alteredBase) Frame(id model.TargetID) (*model.Frame, error) {
	if id == b.missing {
		return nil, errors.New("not in base")
	}
	f, err := b.Indexer.Frame(id)
	if err != nil || id != b.modified {
		return f, err
	}
	f.Source = strings.Replace(f.Source, `"status":   "ok",`, `"status": "fine",`, 1)
	return f, nil
}

func TestDiffTourEndpoint(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	health, err := idx.LookupSymbol("Server.handleHealth")
	if err != nil {
		t.Fatal(err)
	}
	diffTour, err := idx.LookupSymbol("Server.handleDiffTour")
	if err != nil {
		t.Fatal(err)
	}

	srv := New(idx)
	srv.SetTours(tours.NewStore(t.TempDir()))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("off without a base", func(t *testing.T) {
		var resp map[string]string
		getJSON(t, ts.URL+"/api/diff/tour", http.StatusNotFound, &resp)
		if !strings.Contains(resp["error"], "--diff-base") {
			t.Errorf("error should say how to turn diff mode on: %q", resp["error"])
		}
	})

	srv.SetDiffer(diff.New(alteredBase{Indexer: idx, modified: health, missing: diffTour}))
	srv.SetDiffBase("main", "abcdef0123456789")

	var resp struct {
		Base    string `json:"base"`
		Commit  string `json:"commit"`
		Deleted int    `json:"deleted"`
		difftour.Reach
		Tour tours.Tour `json:"tour"`
	}
	getJSON(t, ts.URL+"/api/diff/tour", http.StatusOK, &resp)

	if resp.Base != "main" || resp.Commit != "abcdef0123456789" || resp.Deleted != 0 {
		t.Errorf("header: base=%q commit=%q deleted=%d", resp.Base, resp.Commit, resp.Deleted)
	}
	if !resp.EntrypointsKnown {
		t.Error("the Go engine lists entrypoints")
	}
	if len(resp.Functions) != 2 {
		t.Fatalf("changed = %d functions, want handleHealth and handleDiffTour: %+v", len(resp.Functions), resp.Functions)
	}
	byID := map[model.TargetID]difftour.Function{}
	for _, f := range resp.Functions {
		byID[f.ID] = f
	}
	h := byID[health]
	if h.Status != "modified" || h.Added != 1 || h.Removed != 1 || h.Symbol != "server.Server.handleHealth" {
		t.Errorf("handleHealth change: %+v", h.Change)
	}
	// handleHealth is the /api/health route's handler, so it is an entrypoint
	// in its own right and the route is its nearest way in.
	if len(h.Routes) == 0 || h.Routes[0].Kind != "http.route" || h.Routes[0].Key != "/api/health" || len(h.Routes[0].Path) != 1 {
		t.Errorf("handleHealth routes: %+v", h.Routes)
	}
	d := byID[diffTour]
	if d.Status != "added" || d.Removed != 0 || d.Added < 10 {
		t.Errorf("handleDiffTour change: %+v", d.Change)
	}
	if len(d.Routes) == 0 || d.Routes[0].Key != "/api/diff/tour" {
		t.Errorf("handleDiffTour routes: %+v", d.Routes)
	}

	if resp.Tour.Name != "diff:main" || resp.Tour.Title != "Changes vs main" || !resp.Tour.Generated {
		t.Errorf("tour: %+v", resp.Tour)
	}
	if len(resp.Tour.Stops) != 2 || resp.Tour.Stops[0].Symbol == "" {
		t.Fatalf("stops: %+v", resp.Tour.Stops)
	}
	// Every stop's symbol must resolve back to the function it names.
	for _, s := range resp.Tour.Stops {
		var f model.Frame
		getJSON(t, ts.URL+"/api/symbol?name="+s.Symbol, http.StatusOK, &f)
		if f.ID != health && f.ID != diffTour {
			t.Errorf("stop %q resolved to %q", s.Symbol, f.ID)
		}
	}

	t.Run("skeleton for an author", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/api/diff/tour?format=skeleton")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var sk tours.Tour
		if err := json.NewDecoder(res.Body).Decode(&sk); err != nil {
			t.Fatal(err)
		}
		if sk.Title != "" || sk.Description != "" || sk.Generated || len(sk.Stops) != 2 {
			t.Fatalf("skeleton: %+v", sk)
		}
		for _, s := range sk.Stops {
			if s.Symbol == "server.Server.handleHealth" && !strings.HasPrefix(s.Description, "[[line:+") {
				t.Errorf("modified stop should carry a cue placeholder, got %q", s.Description)
			}
			if s.Symbol == "server.Server.handleDiffTour" && s.Description != "" {
				t.Errorf("added stop should have an empty description, got %q", s.Description)
			}
		}
	})

	t.Run("recomputed after a reload", func(t *testing.T) {
		srv.SetDiffer(diff.New(idx)) // the base now matches head exactly
		var same struct{ Tour tours.Tour }
		getJSON(t, ts.URL+"/api/diff/tour", http.StatusOK, &same)
		if len(same.Tour.Stops) != 2 {
			t.Fatalf("cached answer should survive until a reload, got %d stops", len(same.Tour.Stops))
		}
		srv.NotifyReload()
		var fresh struct{ Tour tours.Tour }
		getJSON(t, ts.URL+"/api/diff/tour", http.StatusOK, &fresh)
		if len(fresh.Tour.Stops) != 0 {
			t.Errorf("after reload nothing differs, got %d stops", len(fresh.Tour.Stops))
		}
	})
}
