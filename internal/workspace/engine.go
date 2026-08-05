package workspace

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

var (
	_ model.Engine         = (*Workspace)(nil)
	_ model.PlatformEngine = (*Workspace)(nil)
)

// Ids crossing this boundary are rewritten in both directions: a sub-engine
// only ever sees its own bare ids, and everything leaving the workspace
// carries its repo. Doing it here rather than inside the indexers keeps the
// single-repo engine unaware that federation exists.

func (w *Workspace) LookupSymbol(name string) (model.TargetID, error) {
	alias, bare := split(name)
	if alias != "" {
		if _, ok := w.repos[alias]; ok {
			idx, _, err := w.engineFor(name)
			if err != nil {
				return "", err
			}
			id, err := idx.LookupSymbol(bare)
			return model.TargetID(w.qualify(alias, string(id))), err
		}
		// Not a repo prefix — a symbol name that merely contains "::".
	}
	idx, _, err := w.engineFor("")
	if err != nil {
		return "", err
	}
	id, err := idx.LookupSymbol(name)
	return model.TargetID(w.qualify(w.primary, string(id))), err
}

func (w *Workspace) Frame(id model.TargetID) (*model.Frame, error) {
	idx, bare, err := w.engineFor(string(id))
	if err != nil {
		return nil, err
	}
	alias, _ := split(string(id))
	if alias == "" {
		alias = w.primary
	}
	f, err := idx.Frame(model.TargetID(bare))
	if err != nil {
		return nil, err
	}
	w.qualifyFrame(alias, f)
	return f, nil
}

func (w *Workspace) FrameForCall(id model.CallID, choice int) (*model.Frame, error) {
	idx, bare, err := w.engineFor(string(id))
	if err != nil {
		return nil, err
	}
	alias, _ := split(string(id))
	if alias == "" {
		alias = w.primary
	}
	f, err := idx.FrameForCall(model.CallID(bare), choice)
	if err != nil {
		return nil, err
	}
	w.qualifyFrame(alias, f)
	return f, nil
}

func (w *Workspace) TypeInfo(id model.TargetID, offset int) (*model.TypeInfo, error) {
	idx, bare, err := w.engineFor(string(id))
	if err != nil {
		return nil, err
	}
	alias, _ := split(string(id))
	if alias == "" {
		alias = w.primary
	}
	ti, err := idx.TypeInfo(model.TargetID(bare), offset)
	if err != nil || ti == nil {
		return ti, err
	}
	ti.TargetID = model.TargetID(w.qualify(alias, string(ti.TargetID)))
	return ti, nil
}

func (w *Workspace) Usages(id model.TargetID) ([]model.Usage, error) {
	idx, bare, err := w.engineFor(string(id))
	if err != nil {
		return nil, err
	}
	alias, _ := split(string(id))
	if alias == "" {
		alias = w.primary
	}
	us, err := idx.Usages(model.TargetID(bare))
	if err != nil {
		return nil, err
	}
	for n := range us {
		us[n].Caller = model.TargetID(w.qualify(alias, string(us[n].Caller)))
		us[n].CallID = model.CallID(w.qualify(alias, string(us[n].CallID)))
	}
	return us, nil
}

// Search covers every repo that is already indexed. Loading the rest would
// turn a keystroke into minutes of compilation, so a lazy workspace searches
// what it has and says so via Repos().
func (w *Workspace) Search(query string, limit int) []model.SearchResult {
	var out []model.SearchResult
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded := r.idx, r.loaded
		r.mu.Unlock()
		if !loaded || idx == nil {
			continue
		}
		for _, res := range idx.Search(query, limit) {
			res.TargetID = model.TargetID(w.qualify(alias, string(res.TargetID)))
			if alias != w.primary {
				res.Label = r.name + " · " + res.Label
			}
			out = append(out, res)
		}
	}
	// Primary-repo hits first: that's the service being read.
	sort.SliceStable(out, func(a, b int) bool {
		return !strings.Contains(string(out[a].TargetID), Sep) &&
			strings.Contains(string(out[b].TargetID), Sep)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Files unions the indexed repos. Paths are absolute, so they need no
// prefixing — and /api/open's containment check works across the workspace
// as a result.
func (w *Workspace) Files() []string {
	var out []string
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded := r.idx, r.loaded
		r.mu.Unlock()
		if loaded && idx != nil {
			out = append(out, idx.Files()...)
		}
	}
	return out
}

func (w *Workspace) qualifyFrame(alias string, f *model.Frame) {
	if f == nil || alias == w.primary {
		return
	}
	f.ID = model.TargetID(w.qualify(alias, string(f.ID)))
	for n := range f.Calls {
		c := &f.Calls[n]
		c.ID = model.CallID(w.qualify(alias, string(c.ID)))
		c.TargetID = model.TargetID(w.qualify(alias, string(c.TargetID)))
		for j := range c.Candidates {
			c.Candidates[j].TargetID = model.TargetID(w.qualify(alias, string(c.Candidates[j].TargetID)))
		}
		for j := range c.Receivers {
			c.Receivers[j].TargetID = model.TargetID(w.qualify(alias, string(c.Receivers[j].TargetID)))
		}
	}
}

// SetProtoRoot re-points every repo's declared surface, and rebuilds the
// cross-repo join that depends on it.
func (w *Workspace) SetProtoRoot(dir string) error {
	w.protoRoot = dir
	w.servedBy = map[string]string{}
	w.readDeclarations()
	var firstErr error
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded := r.idx, r.loaded
		r.mu.Unlock()
		if !loaded || idx == nil {
			continue
		}
		if err := idx.SetProtoRoot(dir); err != nil && alias == w.primary {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Workspace) PlatformAvailable() bool { return true }

// ServiceView describes the primary repo, then annotates its outbound edges
// with the workspace repo that serves each key. Resolution uses only the
// declaration layer, so a large lazy workspace still answers "who serves
// this" instantly — the cost of actually opening the implementation is
// deferred to Resolve.
func (w *Workspace) ServiceView(anchor model.TargetID) (*model.ServiceView, error) {
	idx, bare, err := w.engineFor(string(anchor))
	if err != nil {
		// An anchor in a repo that failed to load shouldn't sink the view.
		idx, _, err = w.engineFor("")
		if err != nil {
			return nil, err
		}
		bare = ""
	}
	// Only an anchor belonging to the primary repo can be marked, since the
	// view is about that service.
	if alias, _ := split(string(anchor)); alias != "" && alias != w.primary {
		bare = ""
	}
	sv, err := idx.ServiceView(model.TargetID(bare))
	if err != nil {
		return nil, err
	}
	sv.Anchor = model.TargetID(w.qualify(w.primary, string(sv.Anchor)))
	sv.Repos = w.Repos()
	for n := range sv.Inbound {
		w.qualifyBinding(w.primary, &sv.Inbound[n])
	}
	for n := range sv.Outbound {
		b := &sv.Outbound[n]
		w.qualifyBinding(w.primary, b)
		if alias, ok := w.servedBy[declKey(b.Kind, b.Key)]; ok && alias != w.primary {
			b.ServedBy = w.repos[alias].name
			b.ServedByRepo = alias
		}
	}
	return sv, nil
}

func (w *Workspace) qualifyBinding(alias string, b *model.Binding) {
	b.Target = model.TargetID(w.qualify(alias, string(b.Target)))
	b.Site = model.TargetID(w.qualify(alias, string(b.Site)))
}

// Resolve opens the implementation of a declared key in whichever repo serves
// it, indexing that repo if this is the first visit. This is the cross-repo
// hop: the key was matched from declarations alone, and only now — when the
// user actually asked to go there — is the Go index paid for.
func (w *Workspace) Resolve(kind, key string) (*model.Resolution, error) {
	alias, ok := w.servedBy[declKey(kind, key)]
	if !ok {
		return nil, fmt.Errorf("no service in this workspace serves %s %q", kind, key)
	}
	r := w.repos[alias]
	if err := w.load(alias); err != nil {
		return nil, err
	}
	r.mu.Lock()
	idx := r.idx
	r.mu.Unlock()

	res := &model.Resolution{Repo: alias, Service: r.name}
	sv, err := idx.ServiceView("")
	if err != nil {
		return nil, err
	}
	for _, b := range sv.Inbound {
		if b.Kind != kind || b.Key != key {
			continue
		}
		res.Target = model.TargetID(w.qualify(alias, string(b.Target)))
		res.Title = b.TargetTitle
		res.Stale = b.Stale
		break
	}
	if res.Target == "" {
		// The serving repo is known, but its implementation isn't linkable —
		// say which repo to look in rather than failing outright.
		res.Note = fmt.Sprintf("%s declares %s but unfold could not identify its implementation", r.name, key)
	}
	return res, nil
}
