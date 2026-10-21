package tours

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListReadsFilesWrittenByHand(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("empty project: got %+v", got)
	}

	// A tour written by something other than the store — the authoring path
	// this package exists for. The file carries no name; the filename is it.
	toursDir := filepath.Join(dir, ".unfold", "tours")
	if err := os.MkdirAll(toursDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "title": "How a publish is retried",
  "stops": [
    {"symbol": "pubsub.(*Client).Publish", "description": "entry point"},
    {"file": "internal/pubsub/retry.go", "line": 41, "description": "the backoff table"},
    {"symbol": "pubsub.(*Client).Publish", "expand": ["retry.Do > backoff.Next"]}
  ]
}`
	if err := os.WriteFile(filepath.Join(toursDir, "retry-path.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Something that is not a tour at all, so the list says so instead of
	// dropping it.
	if err := os.WriteFile(filepath.Join(toursDir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not a tour file; ignored.
	if err := os.WriteFile(filepath.Join(toursDir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := s.List()
	if len(got) != 2 {
		t.Fatalf("got %d tours, want 2: %+v", len(got), got)
	}
	// Sorted by name: broken < retry-path.
	if got[0].Name != "broken" || got[0].Error == "" || len(got[0].Stops) != 0 {
		t.Errorf("broken file: %+v", got[0])
	}
	rp := got[1]
	if rp.Name != "retry-path" || rp.Title != "How a publish is retried" || rp.Error != "" {
		t.Errorf("retry-path: %+v", rp)
	}
	if len(rp.Stops) != 3 || rp.Stops[1].Line != 41 || rp.Stops[2].Expand[0] != "retry.Do > backoff.Next" {
		t.Errorf("stops: %+v", rp.Stops)
	}
	if rp.UpdatedAt == "" {
		t.Errorf("UpdatedAt not set")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		tour Tour
		want string // substring of the error; "" means valid
	}{
		{"no stops", Tour{}, "at least one stop"},
		{"symbol only", Tour{Stops: []Stop{{Symbol: "a.B"}}}, ""},
		{"file+line", Tour{Stops: []Stop{{File: "x.go", Line: 3}}}, ""},
		{"neither", Tour{Stops: []Stop{{Description: "?"}}}, "needs a symbol or a file"},
		{"both", Tour{Stops: []Stop{{Symbol: "a", File: "b"}}}, "not both"},
		{"line on symbol", Tour{Stops: []Stop{{Symbol: "a", Line: 2}}}, "line goes with a file"},
		{"negative line", Tour{Stops: []Stop{{File: "a", Line: -1}}}, "1-based"},
		{"empty expand", Tour{Stops: []Stop{{Symbol: "a", Expand: []string{" "}}}}, "empty expand"},
	}
	for _, c := range cases {
		err := Validate(c.tour)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestSaveRoundTripAndNames(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	saved, err := s.Save(Tour{Name: "auth", Title: "Auth", Stops: []Stop{{Symbol: "Login", Description: "start here"}}})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Name != "auth" || saved.Title != "Auth" || saved.UpdatedAt == "" {
		t.Errorf("saved: %+v", saved)
	}
	// Derived fields don't reach the file; name is the filename.
	buf, err := os.ReadFile(filepath.Join(dir, ".unfold", "tours", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(buf), "updatedAt") || strings.Contains(string(buf), `"name"`) {
		t.Errorf("file carries derived fields: %s", buf)
	}
	got, err := s.Get("auth")
	if err != nil || got.Title != "Auth" {
		t.Errorf("Get: %+v, %v", got, err)
	}
	if _, err := s.Get("nope"); err == nil {
		t.Errorf("Get(nope): want error")
	}

	// A name is a filename stem, never a path.
	for _, bad := range []string{"", "../x", "a/b", ".hidden", "a b"} {
		if _, err := s.Save(Tour{Name: bad, Stops: []Stop{{Symbol: "x"}}}); err == nil {
			t.Errorf("Save(%q): want error", bad)
		}
	}
	// Invalid content doesn't land on disk either.
	if _, err := s.Save(Tour{Name: "empty"}); err == nil {
		t.Errorf("Save(no stops): want error")
	}
	if _, err := os.Stat(filepath.Join(dir, ".unfold", "tours", "empty.json")); err == nil {
		t.Errorf("invalid tour was written")
	}
}

// TestWatchSeesDirectoriesAppear starts watching a project with no .unfold
// at all, then creates the chain and a tour file by hand — the order of
// events when an agent writes the first tour into a fresh project.
func TestWatchSeesDirectoriesAppear(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	changes := make(chan struct{}, 16)
	w, err := s.Watch(func() { changes <- struct{}{} })
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	wait := func(what string) {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(3 * time.Second):
			t.Fatalf("no change seen after %s", what)
		}
	}
	drain := func() {
		for {
			select {
			case <-changes:
			default:
				return
			}
		}
	}

	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "a.json"), []byte(`{"stops":[{"symbol":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wait("creating the directories and first file")
	// Settle: directory creation and the write may have been coalesced into
	// one firing or two; what matters is the next write is seen on its own.
	time.Sleep(300 * time.Millisecond)
	drain()

	if err := os.WriteFile(filepath.Join(s.Dir(), "b.json"), []byte(`{"stops":[{"symbol":"y"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wait("writing a second tour")
	if got := s.List(); len(got) != 2 {
		t.Errorf("List after writes: %+v", got)
	}
}
