package workspace

import (
	"sync"
	"testing"
)

// A repo being read right now is a different state from a repo nobody has
// opened, and the difference is the whole point of showing it: one is "not
// yet", the other is "not unless you ask".
func TestReposReportIndexingWhileItHappens(t *testing.T) {
	var mu sync.Mutex
	// Every state the workspace passed through, sampled from the change hook
	// rather than by polling — polling would race the load it is watching.
	var sawIndexing, sawSettled bool

	prev := OnRepoChange
	t.Cleanup(func() { OnRepoChange = prev })

	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	var w *Workspace
	OnRepoChange = func() {
		mu.Lock()
		defer mu.Unlock()
		if w == nil {
			return
		}
		busy := false
		for _, r := range w.Repos() {
			if r.Indexing {
				busy = true
				// A repo cannot be both mid-read and finished.
				if r.Indexed {
					t.Errorf("%s reports indexing and indexed at once", r.Alias)
				}
			}
		}
		if busy {
			sawIndexing = true
		} else {
			sawSettled = true
		}
	}

	w, err = Open(dirs, abs(t, "testdata/ws/inbox"), abs(t, "testdata/protoroot"), ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()

	mu.Lock()
	defer mu.Unlock()
	if !sawIndexing {
		t.Error("nothing ever reported itself as indexing")
	}
	if !sawSettled {
		t.Error("the workspace never reported itself as settled")
	}
	// And when the dust settles nothing claims to still be working.
	for _, r := range w.Repos() {
		if r.Indexing {
			t.Errorf("%s still reports indexing after WaitIndexed", r.Alias)
		}
		if !r.Indexed && r.Error == "" {
			t.Errorf("%s is neither indexed nor failed after an eager load", r.Alias)
		}
	}
}

// The hook is what lets the UI show progress without polling for it, so it has
// to fire on both edges — a start nobody hears about is a spinner that never
// appears, and an end nobody hears about is one that never stops.
func TestRepoChangeFiresOnBothEdges(t *testing.T) {
	prev := OnRepoChange
	t.Cleanup(func() { OnRepoChange = prev })

	var mu sync.Mutex
	calls := 0
	OnRepoChange = func() {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), abs(t, "testdata/protoroot"), ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()

	mu.Lock()
	defer mu.Unlock()
	// Three repos, each starting and finishing.
	if want := 2 * len(dirs); calls != want {
		t.Errorf("hook fired %d times, want %d (start and end for each of %d repos)", calls, want, len(dirs))
	}
}
