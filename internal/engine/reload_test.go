package engine

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaxInertia/unfold/internal/model"
)

// closerEngine is a fake model.Engine that also implements io.Closer. Each
// delegated call marks itself in-flight; Close records a violation if it runs
// while any call is in-flight — exactly the use-after-close the reload lock
// must prevent. A shared `violated` flag aggregates across the many engines a
// reload sequence swaps through.
type closerEngine struct {
	inCall   atomic.Int32
	violated *atomic.Bool
}

func (f *closerEngine) Frame(model.TargetID) (*model.Frame, error) {
	f.inCall.Add(1)
	defer f.inCall.Add(-1)
	time.Sleep(50 * time.Microsecond) // widen the window a swap could race
	return &model.Frame{}, nil
}

func (f *closerEngine) Close() error {
	if f.inCall.Load() != 0 {
		f.violated.Store(true)
	}
	return nil
}

func (f *closerEngine) LookupSymbol(string) (model.TargetID, error) { return "", nil }
func (f *closerEngine) FrameForCall(model.CallID, int) (*model.Frame, error) {
	return &model.Frame{}, nil
}
func (f *closerEngine) Search(string, int) []model.SearchResult               { return nil }
func (f *closerEngine) Files() []string                                       { return nil }
func (f *closerEngine) TypeInfo(model.TargetID, int) (*model.TypeInfo, error) { return nil, nil }

// TestReloadableNoCallAfterClose drives readers against a Reloadable while a
// swapper repeatedly replaces and Closes the engine the way Reload does. The
// fake records if it is ever Closed mid-call; with the lock held across the
// delegated call, that must never happen. Run with -race.
func TestReloadableNoCallAfterClose(t *testing.T) {
	var violated atomic.Bool
	newFake := func() *closerEngine { return &closerEngine{violated: &violated} }

	r := &Reloadable{cur: newFake()}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = r.Frame("x")
			}
		}()
	}

	// Mirror Reload exactly: build the next engine, take the write lock to
	// swap, release, then Close the old one.
	for i := 0; i < 100; i++ {
		next := newFake()
		r.mu.Lock()
		old := r.cur
		r.cur = next
		r.mu.Unlock()
		_ = old.(io.Closer).Close()
	}

	close(stop)
	wg.Wait()
	_ = r.Close()

	if violated.Load() {
		t.Fatal("an engine was Closed while a delegated call was still in flight (use-after-close)")
	}
}

// TestReloadablePicksUpNewSymbol verifies that Reload rebuilds the index so a
// function added to the source after startup becomes resolvable — the core
// payoff of watch mode.
func TestReloadablePicksUpNewSymbol(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmp\n\ngo 1.21\n")
	src := filepath.Join(dir, "main.go")
	writeFile(t, src, "package main\n\nfunc Alpha() {}\n\nfunc main() { Alpha() }\n")

	r, err := NewReloadable(LangGo, dir, "./...")
	if err != nil {
		t.Fatalf("NewReloadable: %v", err)
	}
	defer r.Close()

	if _, err := r.LookupSymbol("Alpha"); err != nil {
		t.Fatalf("Alpha should resolve before reload: %v", err)
	}
	if _, err := r.LookupSymbol("Beta"); err == nil {
		t.Fatal("Beta should not resolve before it is added")
	}

	// Add Beta and reindex.
	writeFile(t, src, "package main\n\nfunc Alpha() {}\n\nfunc Beta() {}\n\nfunc main() { Alpha(); Beta() }\n")
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := r.LookupSymbol("Beta"); err != nil {
		t.Fatalf("Beta should resolve after reload: %v", err)
	}
}

// TestReloadableConcurrentReadsDuringReload hammers reads from several
// goroutines while reloads swap the engine underneath them. Run with -race,
// it asserts the lock discipline: reads hold the RLock for the whole call, so
// the engine is never swapped/closed mid-read.
func TestReloadableConcurrentReadsDuringReload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmp\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Alpha() {}\n\nfunc main() { Alpha() }\n")

	r, err := NewReloadable(LangGo, dir, "./...")
	if err != nil {
		t.Fatalf("NewReloadable: %v", err)
	}
	defer r.Close()

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_, _ = r.LookupSymbol("Alpha")
				_ = r.Files()
				_ = r.Search("Alpha", 5)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		if err := r.Reload(); err != nil {
			t.Errorf("Reload: %v", err)
		}
	}
	close(done)
	wg.Wait()
}

// TestWatcherFiresOnSourceChange checks that the debounced watcher invokes its
// callback for a source-file edit and ignores non-source files.
func TestWatcherFiresOnSourceChange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	writeFile(t, src, "package a\n")

	fired := make(chan struct{}, 8)
	w, err := NewWatcher(dir, 30*time.Millisecond, func() { fired <- struct{}{} })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	writeFile(t, src, "package a\n\nfunc F() {}\n")
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not fire on a .go change")
	}

	// Drain any coalesced extras, then confirm a non-source file is ignored.
	drain(fired)
	writeFile(t, filepath.Join(dir, "notes.txt"), "scratch\n")
	select {
	case <-fired:
		t.Fatal("watcher fired on a non-source file")
	case <-time.After(400 * time.Millisecond):
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func drain(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
