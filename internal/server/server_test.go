package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		// Resolve main, then follow engine.NewReloadable() from inside it.
		var main indexer.Frame
		getJSON(t, ts.URL+"/api/symbol?name=main", http.StatusOK, &main)
		var loadCallID indexer.CallID
		for _, c := range main.Calls {
			if c.DisplayName == "engine.NewReloadable" {
				loadCallID = c.ID
				break
			}
		}
		if loadCallID == "" {
			t.Fatal("engine.NewReloadable call not found in main frame")
		}
		var callee indexer.Frame
		getJSON(t, ts.URL+"/api/body?callId="+string(loadCallID), http.StatusOK, &callee)
		if !strings.Contains(callee.Source, "func NewReloadable(") {
			t.Errorf("callee source missing 'func NewReloadable(': %s", callee.Source[:minInt(120, len(callee.Source))])
		}
	})

	t.Run("body-rejects-both-params", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/body?targetId=x&callId=y", http.StatusBadRequest)
	})

	t.Run("usages", func(t *testing.T) {
		// NewReloadable is called from cmd/cli's main — at least one caller.
		var main indexer.Frame
		getJSON(t, ts.URL+"/api/symbol?name=NewReloadable", http.StatusOK, &main)
		var resp struct {
			Usages []struct {
				CallID      string `json:"callId"`
				Caller      string `json:"caller"`
				CallerTitle string `json:"callerTitle"`
				Kind        string `json:"kind"`
				Excerpt     string `json:"excerpt"`
			} `json:"usages"`
		}
		getJSON(t, ts.URL+"/api/usages?targetId="+url.QueryEscape(string(main.ID)), http.StatusOK, &resp)
		if len(resp.Usages) == 0 {
			t.Fatal("expected at least one usage of NewReloadable")
		}
		found := false
		for _, u := range resp.Usages {
			if u.Kind == "call" && u.CallerTitle == "main" && u.CallID != "" &&
				strings.Contains(u.Excerpt, "NewReloadable") {
				found = true
			}
		}
		if !found {
			t.Errorf("no call usage from main with excerpt; got %+v", resp.Usages)
		}
	})

	t.Run("usages-missing-param", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/usages", http.StatusBadRequest)
	})

	t.Run("usages-unknown-target", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/usages?targetId=__nope__", http.StatusNotFound)
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

	// /api/open is the one side-effecting endpoint; verify its guards. We use
	// UNFOLD_EDITOR=true so a permitted open runs a harmless no-op binary.
	t.Run("open-guards", func(t *testing.T) {
		t.Setenv("UNFOLD_EDITOR", "true")
		files := srv.engine.Files()
		if len(files) == 0 {
			t.Fatal("no indexed files to test open with")
		}
		anIndexed := url.QueryEscape(files[0])

		// GET is rejected (so a cross-origin <img>/<form> can't trigger it).
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodGet, nil, http.StatusMethodNotAllowed)
		// Cross-site and same-site POSTs are both rejected (the threat includes
		// another app on the same machine, which is same-site).
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden)
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden)
		// Origin-header fallback (no Fetch-Metadata): a mismatched Origin is
		// rejected, a matching one is allowed.
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodPost,
			map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden)
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodPost,
			map[string]string{"Origin": ts.URL}, http.StatusOK)
		// A path outside the indexed project is rejected even with a valid method/origin.
		postStatus(t, ts.URL+"/api/open?file=%2Fetc%2Fpasswd", http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusForbidden)
		// A same-origin POST for an indexed file is allowed.
		postStatus(t, ts.URL+"/api/open?file="+anIndexed, http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK)
	})
}

func postStatus(t *testing.T, rawURL, method string, headers map[string]string, want int) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", rawURL, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d", method, rawURL, resp.StatusCode, want)
	}
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
