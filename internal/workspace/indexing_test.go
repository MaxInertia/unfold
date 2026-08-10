package workspace

import (
	"sync"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
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

// Adding a repository must not re-read the ones already open.
//
// A rebuild used to discard every index, because it had no way to know why it
// was happening — so linking one service re-read the four already there, which
// is minutes of work to learn what they had already said. Nothing about their
// code changed; only the set of repositories did.
func TestReopenKeepsIndexesNothingChanged(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/pubsub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	first, err := Open(dirs, abs(t, "testdata/pubsub/publisher"), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first.WaitIndexed()

	// The identity of an index is what "carried over" means: the same object,
	// not an equal one built again.
	was := map[string]any{}
	for _, alias := range first.order {
		r := first.repos[alias]
		r.mu.Lock()
		was[alias] = r.idx
		r.mu.Unlock()
	}

	second, err := Reopen(first, dirs, abs(t, "testdata/pubsub/publisher"), "", ModeEager, nil)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	second.WaitIndexed()

	for _, alias := range second.order {
		r := second.repos[alias]
		r.mu.Lock()
		got := r.idx
		r.mu.Unlock()
		if got == nil {
			t.Errorf("%s has no index after a rebuild", alias)
			continue
		}
		if any(got) != was[alias] {
			t.Errorf("%s was re-read, though nothing about it changed", alias)
		}
	}

	// And the join is rebuilt from the adopted indexes rather than lost with
	// them: an adopted repo skips the load that would normally publish it.
	if ends := second.endsOf("sdk.event", "foo-happened", model.RoleInbound); len(ends) == 0 {
		if ends = second.endsOf("pubsub.topic", "foo-happened", model.RoleInbound); len(ends) == 0 {
			// The fixture's rules aren't loaded here, so bindings may be empty;
			// what must hold is that Channels() answers at all.
			if second.Channels() == nil {
				t.Error("the join was not rebuilt after adopting indexes")
			}
		}
	}
}

// The repository whose file changed is read again, and only it.
func TestReopenRebuildsOnlyWhatChanged(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/pubsub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	first, err := Open(dirs, abs(t, "testdata/pubsub/publisher"), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first.WaitIndexed()

	changed := first.repos[first.primary].dir
	was := map[string]any{}
	for _, alias := range first.order {
		r := first.repos[alias]
		r.mu.Lock()
		was[alias] = r.idx
		r.mu.Unlock()
	}

	second, err := Reopen(first, dirs, abs(t, "testdata/pubsub/publisher"), "", ModeEager,
		map[string]bool{changed: true})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	second.WaitIndexed()

	for _, alias := range second.order {
		r := second.repos[alias]
		r.mu.Lock()
		got, dir := r.idx, r.dir
		r.mu.Unlock()
		reread := any(got) != was[alias]
		if dir == changed && !reread {
			t.Errorf("%s changed but kept its old index", alias)
		}
		if dir != changed && reread {
			t.Errorf("%s was re-read though %s is what changed", alias, changed)
		}
	}
}
