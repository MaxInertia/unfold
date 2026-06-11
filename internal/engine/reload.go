package engine

import (
	"io"
	"sync"

	"github.com/MaxInertia/unfold/internal/model"
)

// Reloadable wraps a model.Engine and can rebuild it in place when the
// project's source changes. Reads are served by the current engine under a
// read lock; Reload constructs a fresh engine and swaps it atomically, so an
// in-flight request always sees one consistent engine and a failed rebuild
// leaves the previous engine serving.
type Reloadable struct {
	lang   Lang
	dir    string
	target string

	mu  sync.RWMutex
	cur model.Engine
}

// NewReloadable performs the initial load and returns a swappable engine.
func NewReloadable(lang Lang, dir, target string) (*Reloadable, error) {
	eng, err := Load(lang, dir, target)
	if err != nil {
		return nil, err
	}
	return &Reloadable{lang: lang, dir: dir, target: target, cur: eng}, nil
}

// Reload rebuilds the engine from scratch. On success the new engine
// replaces the old one (closing it if it holds resources, e.g. the TS
// sidecar process); on failure the previous engine is kept and the error
// is returned so the caller can log it without disrupting the session.
//
// Reload is safe to run concurrently with reads, but NOT with another
// Reload: two overlapping calls each Load independently and then swap, so
// the engine that wins `cur` is whichever grabs the lock last — which need
// not be the one that started last, leaving a staler engine current. Callers
// must serialize Reload. The Watcher satisfies this: its single debounce
// loop invokes onChange one call at a time.
func (r *Reloadable) Reload() error {
	eng, err := Load(r.lang, r.dir, r.target)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.cur
	r.cur = eng
	r.mu.Unlock()
	if c, ok := old.(io.Closer); ok {
		_ = c.Close()
	}
	return nil
}

// Close releases the current engine.
func (r *Reloadable) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cur.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// model.Engine — every method delegates to the current engine while holding
// the read lock for the *whole* call. This is what makes Reload safe: Reload
// takes the write lock to swap, which blocks until in-flight calls release
// their read lock, so the old engine is never Close()d while a request is
// still talking to it (the TS sidecar would otherwise have its process killed
// mid-roundtrip). Reads still run concurrently with each other; only a reload
// serializes against them.

func (r *Reloadable) LookupSymbol(name string) (model.TargetID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.LookupSymbol(name)
}

func (r *Reloadable) Frame(id model.TargetID) (*model.Frame, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.Frame(id)
}

func (r *Reloadable) FrameForCall(id model.CallID, choice int) (*model.Frame, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.FrameForCall(id, choice)
}

func (r *Reloadable) Search(query string, limit int) []model.SearchResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.Search(query, limit)
}

func (r *Reloadable) Files() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.Files()
}

func (r *Reloadable) TypeInfo(id model.TargetID, offset int) (*model.TypeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.TypeInfo(id, offset)
}

func (r *Reloadable) Usages(id model.TargetID) ([]model.Usage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur.Usages(id)
}
