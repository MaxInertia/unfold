package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/engine"
	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/notes"
)

// TestEndpoints exercises the API against the unfold module itself.
func TestEndpoints(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	srv := New(idx)
	srv.SetTarget("./...")
	srv.SetNotes(notes.NewStore(t.TempDir()))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("health", func(t *testing.T) {
		var resp map[string]any
		getJSON(t, ts.URL+"/api/health", http.StatusOK, &resp)
		if resp["status"] != "ok" {
			t.Errorf("health status: got %q want ok", resp["status"])
		}
		if resp["target"] != "./..." {
			t.Errorf("target: got %q", resp["target"])
		}
		if resp["diff"] != false {
			t.Errorf("diff: got %v want false (no base engine in this test)", resp["diff"])
		}
		// The Go engine has recognizers, so the zoom-out affordance is on.
		if resp["platform"] != true {
			t.Errorf("platform: got %v want true", resp["platform"])
		}
	})

	t.Run("service", func(t *testing.T) {
		var view model.ServiceView
		getJSON(t, ts.URL+"/api/service", http.StatusOK, &view)
		if view.Name != "unfold" {
			t.Errorf("service name: got %q want unfold", view.Name)
		}
		if len(view.Inbound) == 0 {
			t.Fatal("expected unfold's own HTTP routes as its inbound surface")
		}
		var found bool
		for _, b := range view.Inbound {
			if b.Key == "/api/health" {
				found = true
				if b.Target == "" {
					t.Error("/api/health binding should name its handler")
				}
			}
		}
		if !found {
			t.Errorf("missing /api/health among %d inbound bindings", len(view.Inbound))
		}
	})

	t.Run("service-with-anchor", func(t *testing.T) {
		// handleHealth is the /api/health handler, so anchoring on it must
		// mark that route and leave unrelated ones unmarked.
		var anchored model.ServiceView
		id := url.QueryEscape("(*github.com/MaxInertia/unfold/internal/server.Server).handleHealth")
		getJSON(t, ts.URL+"/api/service?anchor="+id, http.StatusOK, &anchored)
		if anchored.Anchor == "" {
			t.Fatal("anchor was not echoed back; the target id may have changed")
		}
		for _, b := range anchored.Inbound {
			if b.Key == "/api/health" && !b.ReachesAnchor {
				t.Error("/api/health should be marked as reaching handleHealth")
			}
			if b.Key == "/api/search" && b.ReachesAnchor {
				t.Error("/api/search should not reach handleHealth")
			}
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

	t.Run("notes-crud", func(t *testing.T) {
		// Create.
		body := `{"anchor":{"file":"/p/a.go","kind":"after-line","startLine":3,"endLine":3,"snippet":"x"},"text":"hello [[NewReloadable]]"}`
		res, err := http.Post(ts.URL+"/api/notes", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		var saved struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(res.Body).Decode(&saved); err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("create: status=%d err=%v", res.StatusCode, err)
		}
		if saved.ID == "" {
			t.Fatal("created note has no id")
		}
		// List.
		var list struct {
			Notes []struct {
				ID string `json:"id"`
			} `json:"notes"`
		}
		getJSON(t, ts.URL+"/api/notes", http.StatusOK, &list)
		if len(list.Notes) != 1 || list.Notes[0].ID != saved.ID {
			t.Fatalf("list: %+v", list)
		}
		// Delete.
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/notes?id="+url.QueryEscape(saved.ID), nil)
		dres, err := http.DefaultClient.Do(req)
		if err != nil || dres.StatusCode != http.StatusOK {
			t.Fatalf("delete: status=%v err=%v", dres, err)
		}
	})

	t.Run("notes-mutations-reject-cross-origin", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/notes", strings.NewReader("{}"))
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site POST: status=%v err=%v", res, err)
		}
	})

	t.Run("typeinfo-describe-target", func(t *testing.T) {
		var main indexer.Frame
		getJSON(t, ts.URL+"/api/symbol?name=NewReloadable", http.StatusOK, &main)
		var resp struct {
			TypeInfo *indexer.TypeInfo `json:"typeInfo"`
		}
		getJSON(t, ts.URL+"/api/typeinfo?targetId="+url.QueryEscape(string(main.ID))+"&offset=-1", http.StatusOK, &resp)
		if resp.TypeInfo == nil {
			t.Fatal("typeinfo offset=-1 returned nil")
		}
		if resp.TypeInfo.Kind != "func" || resp.TypeInfo.Name != "NewReloadable" {
			t.Errorf("describe: got kind=%q name=%q", resp.TypeInfo.Kind, resp.TypeInfo.Name)
		}
		if !strings.Contains(resp.TypeInfo.Type, "func(") {
			t.Errorf("describe type missing signature: %q", resp.TypeInfo.Type)
		}
		if resp.TypeInfo.TargetID != main.ID {
			t.Errorf("describe targetId: got %s want %s", resp.TypeInfo.TargetID, main.ID)
		}
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

		// repo names the service to rank first. It's a hint: a single-repo
		// session has no aliases at all, and a search that came back empty
		// because of one would look like a broken index.
		resp.Results = nil
		getJSON(t, ts.URL+"/api/search?q=Indexer&limit=10&repo=nosuchrepo", http.StatusOK, &resp)
		if len(resp.Results) == 0 {
			t.Error("a repo hint must not filter results away")
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

// TestProtoRootEndpoints covers the browse-and-set flow the UI uses when
// microservice.yaml declares protos but no --proto-root was given: a browser
// can't hand the server a real filesystem path, so the server lists
// directories and the client posts back a choice.
func TestProtoRootEndpoints(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	srv := New(idx)
	srv.SetProjectDir(t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("dirs-lists-subdirectories", func(t *testing.T) {
		var resp struct {
			Path   string   `json:"path"`
			Parent string   `json:"parent"`
			Dirs   []string `json:"dirs"`
		}
		root, err := filepath.Abs("../..")
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		getJSON(t, ts.URL+"/api/dirs?path="+url.QueryEscape(root), http.StatusOK, &resp)
		if resp.Path != root {
			t.Errorf("path: got %q, want %q", resp.Path, root)
		}
		if resp.Parent == "" {
			t.Error("expected a parent so the picker can walk up")
		}
		var foundInternal bool
		for _, d := range resp.Dirs {
			if d == "internal" {
				foundInternal = true
			}
			// Dotfiles are noise in a picker and are filtered out.
			if strings.HasPrefix(d, ".") {
				t.Errorf("hidden directory %q should not be listed", d)
			}
		}
		if !foundInternal {
			t.Errorf("expected unfold's own internal/ among %v", resp.Dirs)
		}
	})

	t.Run("dirs-rejects-cross-origin", func(t *testing.T) {
		// Listing directories outside the project can't use the containment
		// check that guards /api/open, so the origin guard is what stops
		// another page in the browser from walking the filesystem.
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/dirs", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site GET: status=%v err=%v", res, err)
		}
	})

	t.Run("proto-root-rejects-a-non-directory", func(t *testing.T) {
		res, err := http.Post(ts.URL+"/api/proto-root", "application/json",
			strings.NewReader(`{"path":"/definitely/not/a/directory"}`))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("status: got %d, want 400", res.StatusCode)
		}
	})

	t.Run("proto-root-requires-post", func(t *testing.T) {
		getStatus(t, ts.URL+"/api/proto-root", http.StatusMethodNotAllowed)
	})

	t.Run("proto-root-rejects-cross-origin", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/proto-root",
			strings.NewReader(`{"path":"/tmp"}`))
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site POST: status=%v err=%v", res, err)
		}
	})

	t.Run("proto-root-accepts-a-directory", func(t *testing.T) {
		// unfold's own repo has no microservice.yaml, so there's nothing to
		// resolve — setting a valid directory still succeeds.
		dir := t.TempDir()
		res, err := http.Post(ts.URL+"/api/proto-root", "application/json",
			strings.NewReader(`{"path":`+strconv.Quote(dir)+`}`))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		if got := idx.ProtoRoot(); got != dir {
			t.Errorf("engine proto root: got %q, want %q", got, dir)
		}
	})
}

// Linking a repository mid-session is guarded the same way the other mutating
// endpoints are, and validates before it commits — discovering a bad path
// after the rebuild would mean reporting a failure against an engine that had
// already been replaced, and the user would have paid the reindex for nothing.
func TestLinkRepoValidatesBeforeRebuilding(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}
	srv := New(idx)
	// The linked set is package-level state on the engine, so a test that
	// leaves entries behind changes what the next one loads.
	t.Cleanup(func() { engine.LinkedRepos = nil })
	reloads := 0
	srv.SetReloader(func() error { reloads++; return nil })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(t *testing.T, body string, want int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/repos", strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", ts.URL)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("status: got %d want %d", res.StatusCode, want)
		}
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out
	}

	t.Run("rejects a non-module", func(t *testing.T) {
		before := reloads
		post(t, `{"path":"`+t.TempDir()+`"}`, http.StatusBadRequest)
		if reloads != before {
			t.Error("a directory with no go.mod must be rejected before any rebuild")
		}
	})

	t.Run("rejects a missing directory", func(t *testing.T) {
		before := reloads
		post(t, `{"path":"`+filepath.Join(t.TempDir(), "nope")+`"}`, http.StatusBadRequest)
		if reloads != before {
			t.Error("a path that doesn't exist must not trigger a rebuild")
		}
	})

	t.Run("links a real module, once", func(t *testing.T) {
		dir := repoFixture(t)
		before := reloads
		post(t, `{"path":"`+dir+`"}`, http.StatusOK)
		if reloads != before+1 {
			t.Errorf("linking should rebuild exactly once, got %d", reloads-before)
		}
		// The same repo twice is a no-op, not a second copy in the workspace.
		post(t, `{"path":"`+dir+`"}`, http.StatusConflict)
		if reloads != before+1 {
			t.Error("re-linking an open repo must not rebuild")
		}
		post(t, `{"path":"`+dir+`","unlink":true}`, http.StatusOK)
	})

	t.Run("unlinking something not linked is refused", func(t *testing.T) {
		before := reloads
		post(t, `{"path":"`+repoFixture(t)+`","unlink":true}`, http.StatusBadRequest)
		if reloads != before {
			t.Error("no rebuild for a repo that was never linked")
		}
	})

	t.Run("a failed rebuild rolls the link back", func(t *testing.T) {
		dir := repoFixture(t)
		srv.SetReloader(func() error { return errFake })
		post(t, `{"path":"`+dir+`"}`, http.StatusUnprocessableEntity)
		// The engine kept the previous index, so the link must not survive —
		// otherwise the next rebuild for any other reason would silently
		// apply a repo the user was told had failed.
		srv.SetReloader(func() error { reloads++; return nil })
		post(t, `{"path":"`+dir+`","unlink":true}`, http.StatusBadRequest)
	})

	t.Run("guards", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/api/repos")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET should be refused, got %d", res.StatusCode)
		}
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/repos", strings.NewReader(`{"path":"/tmp"}`))
		req.Header.Set("Origin", "http://evil.example")
		res2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		res2.Body.Close()
		if res2.StatusCode != http.StatusForbidden {
			t.Errorf("cross-origin should be refused, got %d", res2.StatusCode)
		}
	})
}

var errFake = errors.New("index failed")

// repoFixture makes a throwaway directory that looks like a Go module.
func repoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}
