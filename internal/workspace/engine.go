package workspace

import (
	"fmt"
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
)

var (
	_ model.Engine          = (*Workspace)(nil)
	_ model.PlatformEngine  = (*Workspace)(nil)
	_ model.WorkspaceEngine = (*Workspace)(nil)
	_ model.ServiceSearcher = (*Workspace)(nil)
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

// Search covers every repo that is already indexed, ranked for the primary
// service. See SearchFrom.
func (w *Workspace) Search(query string, limit int) []model.SearchResult {
	return w.SearchFrom("", query, limit)
}

// SearchFrom is Search ranked for whichever service the reader is currently
// in, which above the frame level need not be the repo unfold was launched
// in. Loading the un-indexed repos would turn a keystroke into minutes of
// compilation, so a lazy workspace searches what it has and says so via
// Repos().
//
// Three tiers, and the middle one is the point: the current service's own
// code, then every other indexed service's own code, then dependencies from
// anywhere. A dep of the service you're reading is still someone else's
// implementation — it belongs below a sibling service's real code, not above
// it because it happens to share a repo with you.
func (w *Workspace) SearchFrom(repo, query string, limit int) []model.SearchResult {
	if repo == "" {
		repo = w.primary
	}
	if _, ok := w.repos[repo]; !ok {
		repo = w.primary
	}
	if limit <= 0 {
		limit = 50
	}
	type hit struct {
		res  model.SearchResult
		tier int
	}
	var hits []hit
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
			tier := 1
			switch {
			case res.External:
				tier = 2
			case alias == repo:
				tier = 0
			}
			hits = append(hits, hit{res: res, tier: tier})
		}
	}
	// Stable, so each sub-engine's own ranking survives inside a tier.
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].tier < hits[b].tier })
	out := make([]model.SearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.res)
		if len(out) == limit {
			break
		}
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
		// Same aliasing hazard as bindings: a frame's Candidates and
		// Receivers slices are the engine's, not ours.
		c.Candidates = w.qualifyCandidates(alias, c.Candidates)
		if len(c.Receivers) > 0 {
			recv := make([]model.Receiver, len(c.Receivers))
			for j, r := range c.Receivers {
				r.TargetID = model.TargetID(w.qualify(alias, string(r.TargetID)))
				recv[j] = r
			}
			c.Receivers = recv
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
	return w.ServiceViewOf(w.primary, anchor)
}

// ServiceViewOf describes any service in the workspace. Selecting one at the
// platform level has to be able to zoom into *that* service, so the view
// isn't hard-wired to the repo unfold was launched in.
func (w *Workspace) ServiceViewOf(repo string, anchor model.TargetID) (*model.ServiceView, error) {
	if repo == "" {
		repo = w.primary
	}
	if _, ok := w.repos[repo]; !ok {
		return nil, fmt.Errorf("unknown service %q", repo)
	}
	if err := w.load(repo); err != nil {
		return nil, err
	}
	r := w.repos[repo]
	r.mu.Lock()
	idx := r.idx
	r.mu.Unlock()
	if idx == nil {
		return nil, fmt.Errorf("%s is not indexed", repo)
	}

	// An anchor only means something to the service it belongs to; marking
	// one service's entrypoints with another's frame would be nonsense.
	bare := ""
	if alias, b := split(string(anchor)); (alias == "" && repo == w.primary) || alias == repo {
		bare = b
	}

	sv, err := idx.ServiceView(model.TargetID(bare))
	if err != nil {
		return nil, err
	}
	sv.Anchor = model.TargetID(w.qualify(repo, string(sv.Anchor)))
	sv.Repos = w.Repos()
	for n := range sv.Inbound {
		w.qualifyBinding(repo, &sv.Inbound[n])
	}
	for n := range sv.Outbound {
		b := &sv.Outbound[n]
		w.qualifyBinding(repo, b)
		if alias, ok := w.servedBy[declKey(b.Kind, b.Key)]; ok && alias != repo {
			b.ServedBy = w.repos[alias].name
			b.ServedByRepo = alias
		}
	}
	return sv, nil
}

func (w *Workspace) qualifyBinding(alias string, b *model.Binding) {
	b.Target = model.TargetID(w.qualify(alias, string(b.Target)))
	b.Site = model.TargetID(w.qualify(alias, string(b.Site)))
	b.Candidates = w.qualifyCandidates(alias, b.Candidates)
}

// qualifyCandidates returns a *new* slice. Copying is the whole point: a
// Binding is handed out by value but its Candidates slice header still points
// at the array the indexer stored, so rewriting in place would prefix the
// engine's own data — and prefix it again on the next request, compounding
// until ids match nothing. Single-call tests never see it; a UI that refetches
// does, immediately.
func (w *Workspace) qualifyCandidates(alias string, in []model.Candidate) []model.Candidate {
	if len(in) == 0 {
		return in
	}
	out := make([]model.Candidate, len(in))
	for n, c := range in {
		c.TargetID = model.TargetID(w.qualify(alias, string(c.TargetID)))
		out[n] = c
	}
	return out
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
		// With several implementations there's no single answer, so hand
		// them all back and let the caller choose rather than silently
		// picking one.
		for _, c := range b.Candidates {
			res.Candidates = append(res.Candidates, model.Candidate{
				TargetID: model.TargetID(w.qualify(alias, string(c.TargetID))),
				Label:    c.Label,
			})
		}
		break
	}
	switch {
	case res.Target != "" || len(res.Candidates) > 0:
	default:
		// The serving repo is known, but its implementation isn't linkable —
		// say which repo to look in rather than failing outright.
		res.Note = fmt.Sprintf("%s declares %s but unfold could not identify its implementation", r.name, key)
	}
	return res, nil
}

// PlatformView is the L0 view: every service in the workspace, and the calls
// between them.
//
// Services come from the declaration layer, so all of them appear however
// little has been indexed. Edges can't work that way — knowing that A calls B
// means having read A's code — so an un-indexed service contributes no
// outgoing edges. It still receives them, since those come from other
// services' code, and the view marks it so a lazily-loaded workspace doesn't
// read as "this service calls nothing".
func (w *Workspace) PlatformView(anchor model.TargetID) (*model.PlatformView, error) {
	// Which RPCs of which service lead to the anchor. Computed once, from the
	// anchor's own repo, and then used to mark the edges pointing at it — the
	// platform-level answer to the question the service level answers with
	// entrypoints: what would have to run for this code to run.
	anchorRepo, reachingKeys := w.keysReachingAnchor(anchor)

	pv := &model.PlatformView{
		Services: make([]model.PlatformService, 0, len(w.order)),
		Edges:    []model.PlatformEdge{},
	}
	if anchorRepo != "" {
		pv.Anchor = anchor
		if sv, err := w.ServiceViewOf(anchorRepo, anchor); err == nil {
			pv.AnchorTitle = sv.AnchorTitle
		}
	}
	// grouped is keyed by from→to→kind so several calls between the same
	// pair collapse into one edge carrying its call sites.
	grouped := map[[3]string][]model.PlatformCall{}
	// crossings[alias][outboundKey] = the inbound keys of that service which
	// lead to that outbound call. Per repo, from the crossing relation.
	crossings := map[string]map[string]map[string]bool{}

	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded, loadErr := r.idx, r.loaded, r.err
		r.mu.Unlock()

		svc := model.PlatformService{
			Alias:   alias,
			Name:    r.name,
			Dir:     r.dir,
			Primary: alias == w.primary,
			Indexed: loaded,
			Methods: len(r.methods),
		}
		if loadErr != nil {
			svc.Error = loadErr.Error()
		}
		pv.Services = append(pv.Services, svc)

		if !loaded || idx == nil {
			continue
		}
		sv, err := idx.ServiceView("")
		if err != nil {
			continue
		}
		// The crossing relation, lifted to keys: which of this service's own
		// inbound RPCs lead to each call it makes. This is what carries a mark
		// past the first hop — without it we know inbox calls conversation,
		// but not that serving inbox's own ShowThread is what causes it.
		outKeyOf := map[string]string{}
		for _, b := range sv.Outbound {
			outKeyOf[b.ID] = declKey(b.Kind, b.Key)
		}
		for _, b := range sv.Inbound {
			inKey := declKey(b.Kind, b.Key)
			for _, id := range b.Reaches {
				out, ok := outKeyOf[id]
				if !ok {
					continue
				}
				if crossings[alias] == nil {
					crossings[alias] = map[string]map[string]bool{}
				}
				if crossings[alias][out] == nil {
					crossings[alias][out] = map[string]bool{}
				}
				crossings[alias][out][inKey] = true
			}
		}
		for _, b := range sv.Outbound {
			to, ok := w.servedBy[declKey(b.Kind, b.Key)]
			if !ok || to == alias {
				continue // nothing here serves it, or it's a self-call
			}
			k := [3]string{alias, to, b.Kind}
			grouped[k] = append(grouped[k], model.PlatformCall{
				Key:       b.Key,
				Site:      model.TargetID(w.qualify(alias, string(b.Site))),
				SiteTitle: b.SiteTitle,
				File:      b.File,
				Line:      b.Line,
			})
		}
	}

	byAlias := map[string]*model.PlatformService{}
	for n := range pv.Services {
		byAlias[pv.Services[n].Alias] = &pv.Services[n]
	}

	// Which inbound keys of each service lead to the anchor, and which
	// services do. Marking used to stop after one hop — only edges pointing
	// *into* the anchor's own repo were ever considered — so a service that
	// reached the anchor through an intermediate read as unrelated, which is
	// the opposite of what the level is for.
	//
	// Propagation is a fixpoint rather than a walk because the service graph
	// has cycles: an edge can be marked, then later gain a reason to mark its
	// caller, and the key set is finite so repeating until nothing changes
	// terminates. Two things are tracked, and conflating them would be wrong:
	// a service can reach the anchor via a call made from its own init or
	// main, with no inbound key responsible — it is marked, but there is
	// nothing for *its* callers to inherit.
	reaching := map[string]map[string]bool{}
	serviceReaches := map[string]bool{}
	if anchorRepo != "" {
		reaching[anchorRepo] = reachingKeys
		serviceReaches[anchorRepo] = true
	}
	if anchorRepo != "" {
		for changed := true; changed; {
			changed = false
			for k, calls := range grouped {
				from, to, kind := k[0], k[1], k[2]
				dest := reaching[to]
				if dest == nil {
					continue
				}
				for _, c := range calls {
					key := declKey(kind, c.Key)
					if !dest[key] {
						continue
					}
					if !serviceReaches[from] {
						serviceReaches[from] = true
						changed = true
					}
					// What in `from` causes this call is what its own callers
					// would have to hit — that's the next hop's seed.
					for inKey := range crossings[from][key] {
						if reaching[from] == nil {
							reaching[from] = map[string]bool{}
						}
						if !reaching[from][inKey] {
							reaching[from][inKey] = true
							changed = true
						}
					}
				}
			}
		}
	}

	for k, calls := range grouped {
		sort.Slice(calls, func(a, b int) bool { return calls[a].Key < calls[b].Key })
		e := model.PlatformEdge{From: k[0], To: k[1], Kind: k[2], Calls: calls}
		if dest := reaching[k[1]]; dest != nil {
			for n := range e.Calls {
				if dest[declKey(e.Kind, e.Calls[n].Key)] {
					e.Calls[n].ReachesAnchor = true
					e.ReachesAnchor = true
				}
			}
		}
		pv.Edges = append(pv.Edges, e)
	}
	for alias := range serviceReaches {
		if s := byAlias[alias]; s != nil {
			s.ReachesAnchor = true
		}
	}
	sort.Slice(pv.Edges, func(a, b int) bool {
		if pv.Edges[a].From != pv.Edges[b].From {
			return pv.Edges[a].From < pv.Edges[b].From
		}
		return pv.Edges[a].To < pv.Edges[b].To
	})
	return pv, nil
}

// keysReachingAnchor returns the anchor's repo and the set of that repo's
// inbound keys whose implementation reaches the anchor.
//
// It reuses the service view rather than re-deriving reachability: the same
// backwards walk that answers "which entrypoints run this code" at L1 answers
// "which of my APIs lead here" at L0. Only the framing changes.
func (w *Workspace) keysReachingAnchor(anchor model.TargetID) (string, map[string]bool) {
	if anchor == "" {
		return "", nil
	}
	alias, _ := split(string(anchor))
	if alias == "" {
		alias = w.primary
	}
	if _, ok := w.repos[alias]; !ok {
		return "", nil
	}
	sv, err := w.ServiceViewOf(alias, anchor)
	if err != nil || sv.Anchor == "" {
		return "", nil
	}
	keys := map[string]bool{}
	for _, b := range sv.Inbound {
		if b.ReachesAnchor {
			keys[declKey(b.Kind, b.Key)] = true
		}
	}
	return alias, keys
}

// IndexRepo loads one service's code on demand, so the platform view can be
// filled in a service at a time instead of paying for the whole workspace.
func (w *Workspace) IndexRepo(alias string) error { return w.load(alias) }

// RuleReport describes the recognizers in force, taken from the primary repo.
// Rules are shared across the workspace, so one repo's view of them is the
// workspace's — except for match counts, which are that repo's own.
func (w *Workspace) RuleReport() model.RuleReport {
	r, ok := w.repos[w.primary]
	if !ok {
		return model.RuleReport{}
	}
	r.mu.Lock()
	idx := r.idx
	r.mu.Unlock()
	if idx == nil {
		return model.RuleReport{}
	}
	return idx.RuleReport()
}
