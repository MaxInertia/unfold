package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/indexer"
)

// TestEndpoints exercises the API against the unfold module itself.
func TestEndpoints(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	srv := New(idx)
	srv.SetTarget("./...")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("health", func(t *testing.T) {
		var resp map[string]string
		getJSON(t, ts.URL+"/api/health", http.StatusOK, &resp)
		if resp["status"] != "ok" {
			t.Errorf("health status: got %q want ok", resp["status"])
		}
		if resp["target"] != "./..." {
			t.Errorf("target: got %q", resp["target"])
		}
	})

	t.Run("symbol-by-bare-name", func(t *testing.T) {
		var frame indexer.Frame
		getJSON(t, ts.URL+"/api/symbol?name=main", http.StatusOK, &frame)
		if !strings.Contains(frame.Source, "func main()") {
			t.Errorf("frame source missing 'func main()'")
		}
		if len(frame.Calls) == 0 {
			t.Error("expected calls in main")
		}
	})

	t.Run("symbol-missing-name", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/symbol", http.StatusBadRequest)
	})

	t.Run("symbol-not-found", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/symbol?name=__no_such_function__", http.StatusNotFound)
	})

	t.Run("body-by-targetId-then-follow-call", func(t *testing.T) {
		// Resolve main, then follow engine.Load() from inside it.
		var main indexer.Frame
		getJSON(t, ts.URL+"/api/symbol?name=main", http.StatusOK, &main)
		var loadCallID indexer.CallID
		for _, c := range main.Calls {
			if c.DisplayName == "engine.Load" {
				loadCallID = c.ID
				break
			}
		}
		if loadCallID == "" {
			t.Fatal("engine.Load call not found in main frame")
		}
		var callee indexer.Frame
		getJSON(t, ts.URL+"/api/body?callId="+string(loadCallID), http.StatusOK, &callee)
		if !strings.Contains(callee.Source, "func Load(") {
			t.Errorf("callee source missing 'func Load(': %s", callee.Source[:minInt(120, len(callee.Source))])
		}
	})

	t.Run("body-rejects-both-params", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/body?targetId=x&callId=y", http.StatusBadRequest)
	})

	t.Run("search", func(t *testing.T) {
		var resp struct {
			Results []indexer.SearchResult `json:"results"`
		}
		getJSON(t, ts.URL+"/api/search?q=Indexer&limit=10", http.StatusOK, &resp)
		if len(resp.Results) == 0 {
			t.Error("expected at least one result for q=Indexer")
		}
	})
}

func getJSON(t *testing.T, url string, wantStatus int, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func getStatus(t *testing.T, url string, want int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status %d, want %d", url, resp.StatusCode, want)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
