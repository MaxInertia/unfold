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
func (r *Reloadable) Reload() error { return r.rebuild(nil) }

// ReloadKeeping rebuilds, discarding only the indexes of the named repository
// directories and carrying the rest over. Naming none keeps everything, which
// is right for a rebuild caused by something that changed no code at all —
// a repository being linked.
//
// Reload itself keeps nothing, and that is the correct default for the caller
// that has no idea what changed: a stale index is a wrong answer, where a
// re-read is only a slow one.
func (r *Reloadable) ReloadKeeping(rebuild ...string) error {
	set := make(map[string]bool, len(rebuild))
	for _, d := range rebuild {
		if d != "" {
			set[d] = true
		}
	}
	return r.rebuild(set)
}

func (r *Reloadable) rebuild(discard map[string]bool) error {
	r.mu.RLock()
	prev := r.cur
	r.mu.RUnlock()
	eng, err := Rebuild(prev, r.lang, r.dir, r.target, discard)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.cur
	r.cur = eng
	r.mu.Unlock()
	// Closing the old engine would close indexes the new one just adopted, so
	// only an engine that handed nothing over can be closed. A workspace holds
	// no closable resource of its own; the TS engine's sidecar process is the
	// case this protects, and it never adopts.
	if c, ok := old.(io.Closer); ok && eng != old && !adopted(old, eng) {
		_ = c.Close()
	}
	return nil
}

// adopted reports whether the new engine took anything over from the old one.
func adopted(old, next model.Engine) bool {
	_, wasWorkspace := old.(interface{ Repos() []model.RepoInfo })
	_, isWorkspace := next.(interface{ Repos() []model.RepoInfo })
	return wasWorkspace && isWorkspace
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

// SearchFrom biases search toward one workspace service when the engine held
// can do that, and is plain Search otherwise — a single repo has exactly one
// service, so "rank mine first" is already what it does.
func (r *Reloadable) SearchFrom(repo, query string, limit int) []model.SearchResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ss, ok := r.cur.(model.ServiceSearcher); ok {
		return ss.SearchFrom(repo, query, limit)
	}
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

// ServiceView forwards to the current engine when it has recognizers. The
// wrapper always satisfies model.PlatformEngine (method sets are static), so
// an engine without them is reported at call time.
func (r *Reloadable) ServiceView(anchor model.TargetID) (*model.ServiceView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pe, ok := r.cur.(model.PlatformEngine)
	if !ok {
		return nil, model.ErrNoPlatformView
	}
	return pe.ServiceView(anchor)
}

// SetProtoRoot forwards to the current engine and updates the package-level
// default, so the choice survives the engine rebuilds watch mode performs.
func (r *Reloadable) SetProtoRoot(dir string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pr, ok := r.cur.(interface{ SetProtoRoot(string) error })
	if !ok {
		return model.ErrNoPlatformView
	}
	err := pr.SetProtoRoot(dir)
	// Record it even on failure: the user picked it, and a reload shouldn't
	// silently revert to a different directory than the one on screen.
	ProtoRoot = dir
	return err
}

// Resolve forwards the cross-repo hop to the current engine when it
// federates repositories.
func (r *Reloadable) Resolve(kind, key string, role model.BindingRole) (*model.Resolution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cr, ok := r.cur.(model.CrossRepoResolver)
	if !ok {
		return nil, model.ErrNoWorkspace
	}
	return cr.Resolve(kind, key, role)
}

// PlatformView forwards the workspace-level view when one is open.
func (r *Reloadable) PlatformView(anchor model.TargetID) (*model.PlatformView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	we, ok := r.cur.(model.WorkspaceEngine)
	if !ok {
		return nil, model.ErrNoWorkspace
	}
	return we.PlatformView(anchor)
}

// ServiceViewOf forwards the view of a named workspace service.
func (r *Reloadable) ServiceViewOf(repo string, anchor model.TargetID) (*model.ServiceView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	we, ok := r.cur.(model.WorkspaceEngine)
	if !ok {
		return nil, model.ErrNoWorkspace
	}
	return we.ServiceViewOf(repo, anchor)
}

// IndexRepo forwards on-demand indexing of one workspace service.
func (r *Reloadable) IndexRepo(alias string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ir, ok := r.cur.(interface{ IndexRepo(string) error })
	if !ok {
		return model.ErrNoWorkspace
	}
	return ir.IndexRepo(alias)
}

// Channels forwards the key index when the engine holds one. A single repo
// has channels too — its own bindings — but no other end to name, so the
// question only means something for a workspace.
func (r *Reloadable) Channels() []model.Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lister, ok := r.cur.(model.ChannelLister)
	if !ok {
		return nil
	}
	return lister.Channels()
}

// Repos forwards the workspace's repository list. Empty for a single-repo
// engine, which is the honest answer rather than an error: nothing is wrong,
// there is simply no workspace yet — and linking a repo is exactly how one
// comes to exist mid-session.
func (r *Reloadable) Repos() []model.RepoInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lister, ok := r.cur.(interface{ Repos() []model.RepoInfo })
	if !ok {
		return nil
	}
	return lister.Repos()
}

// RuleReport forwards the recognizer picture for whatever engine is held.
func (r *Reloadable) RuleReport() model.RuleReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep, ok := r.cur.(interface{ RuleReport() model.RuleReport })
	if !ok {
		return model.RuleReport{}
	}
	return rep.RuleReport()
}

// PlatformAvailable reports whether the engine currently held can serve a
// service view, so /api/health advertises the zoom-out affordance honestly
// even though the wrapper's own method set can't.
func (r *Reloadable) PlatformAvailable() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.cur.(model.PlatformEngine)
	return ok
}

// WorkspaceAvailable reports whether the engine currently held is a
// workspace, for the same reason.
func (r *Reloadable) WorkspaceAvailable() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.cur.(model.WorkspaceEngine)
	return ok
}
