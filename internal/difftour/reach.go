// Package difftour turns the set of functions a branch changed into a
// reading: which entrypoints reach each one, and the order execution would
// visit them in. Both the generated "Changes vs <base>" tour and the blast
// radius shown on a changed frame's header come from this one computation,
// so they can't disagree about what reaches what.
//
// The graph it walks is the engine's own: callers through Usages (the same
// reverse index the callers tree reads) and callees through Frame.Calls, so
// the reach it reports is exactly the reach the UI can show by expanding.
package difftour

import (
	"sort"
	"strings"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/model"
)

// Route is one way execution gets from an entrypoint to a changed function:
// the entrypoint, and the shortest call path from it, inclusive at both ends.
type Route struct {
	model.Entrypoint
	Path []model.TargetID `json:"path"`
}

// Function is one changed function with its reach.
type Function struct {
	diff.Change
	// Symbol is a short name that resolves back to the function as a tour
	// symbol, an expand entry or a [[ref]]: "pkg.Func" or "pkg.Type.Method".
	Symbol string `json:"symbol"`
	// Routes lists the entrypoints that reach the function, nearest first.
	// Empty means nothing recognized as a way in leads here.
	Routes []Route `json:"entrypoints"`
	// Expand pre-opens the other changed functions this one leads to, each
	// as the call path from here, so a stop on this function shows the
	// change in the context it runs in.
	Expand []string `json:"expand,omitempty"`
}

// Reach is the ordered answer for one branch.
type Reach struct {
	// EntrypointsKnown is false when the engine can't name where execution
	// starts; every function is then "unreached", which is an absence of
	// knowledge rather than a finding, and the UI says so.
	EntrypointsKnown bool `json:"entrypointsKnown"`
	// Functions is every changed function in execution order: the
	// entrypoint reaching the most changes first, then its callees in call
	// order along the paths, then the next entrypoint's; a function reached
	// from several is listed once, where it is first met. Functions no
	// entrypoint reaches close the list in source order.
	Functions []Function `json:"functions"`
	Unreached int        `json:"unreached"`
	// Names gives the short symbol for every id that appears in a path, so a
	// consumer can print or expand a path without a lookup per hop.
	Names map[model.TargetID]string `json:"names"`
}

// Compute orders changes by their reach from eng's entrypoints.
func Compute(eng model.Engine, changes []diff.Change) *Reach {
	g := newGraph(eng)
	r := &Reach{Names: map[model.TargetID]string{}}

	var eps []model.Entrypoint
	if l, ok := eng.(model.EntrypointLister); ok {
		eps = l.Entrypoints()
		r.EntrypointsKnown = true
	}
	epAt := map[model.TargetID]int{}
	for n, e := range eps {
		if _, dup := epAt[e.ID]; !dup {
			epAt[e.ID] = n
		}
	}

	changed := map[model.TargetID]int{}
	fns := make([]Function, len(changes))
	for n, c := range changes {
		changed[c.ID] = n
		fns[n] = Function{Change: c, Symbol: ShortName(c.ID)}
		r.Names[c.ID] = fns[n].Symbol
	}

	// Backwards from every change: the entrypoints that reach it and the
	// shortest path from each. onPath collects every function on any such
	// path; it bounds the forward walk below to the part of the graph that
	// matters.
	onPath := map[model.TargetID]bool{}
	reachedBy := map[model.TargetID]int{} // entrypoint → changes it reaches
	for n := range fns {
		fns[n].Routes = routesTo(g, fns[n].ID, eps, epAt)
		for _, rt := range fns[n].Routes {
			reachedBy[rt.ID]++
			for _, id := range rt.Path {
				onPath[id] = true
				if _, ok := r.Names[id]; !ok {
					r.Names[id] = ShortName(id)
				}
			}
		}
	}

	// Entrypoint order: the one that tells the most of the story first,
	// source order to break ties.
	order := make([]model.Entrypoint, 0, len(reachedBy))
	for _, e := range eps {
		if reachedBy[e.ID] > 0 {
			order = append(order, e)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return reachedBy[order[a].ID] > reachedBy[order[b].ID]
	})

	// Forwards from each entrypoint, callees in call order, staying on the
	// paths found above. A change is listed where it is first met.
	var out []Function
	visited := map[model.TargetID]bool{}
	var walk func(id model.TargetID)
	walk = func(id model.TargetID) {
		if visited[id] || !onPath[id] {
			return
		}
		visited[id] = true
		if n, ok := changed[id]; ok {
			out = append(out, fns[n])
		}
		for _, callee := range g.callees(id) {
			walk(callee)
		}
	}
	for _, e := range order {
		walk(e.ID)
	}
	// A path found backwards that the forward walk didn't cover would mean
	// the two indexes disagree; list the function anyway rather than lose it.
	for _, f := range fns {
		if len(f.Routes) > 0 && !visited[f.ID] {
			visited[f.ID] = true
			out = append(out, f)
		}
	}
	for _, f := range fns {
		if len(f.Routes) == 0 {
			out = append(out, f)
			r.Unreached++
		}
	}

	// Expand: for each listed function, the changed functions whose nearest
	// path runs through it, as the path from here on.
	for n := range out {
		out[n].Expand = expandFrom(out[n].ID, out, r.Names)
	}
	r.Functions = out
	return r
}

// routesTo walks callers outwards from target and returns every entrypoint
// met, nearest first, each with the shortest path back down to target.
// Value references are not followed: a function passed as a value has no
// call site, so a chain through one isn't an execution path (the same
// limitation the callers tree documents).
func routesTo(g *graph, target model.TargetID, eps []model.Entrypoint, epAt map[model.TargetID]int) []Route {
	next := map[model.TargetID]model.TargetID{} // caller → the callee it was reached from
	seen := map[model.TargetID]bool{target: true}
	queue := []model.TargetID{target}
	var found []model.TargetID
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, ok := epAt[cur]; ok {
			found = append(found, cur)
		}
		for _, caller := range g.callers(cur) {
			if seen[caller] {
				continue
			}
			seen[caller] = true
			next[caller] = cur
			queue = append(queue, caller)
		}
	}
	routes := make([]Route, 0, len(found))
	for _, id := range found {
		path := []model.TargetID{id}
		for cur := id; cur != target; {
			cur = next[cur]
			path = append(path, cur)
		}
		routes = append(routes, Route{Entrypoint: eps[epAt[id]], Path: path})
	}
	return routes
}

// expandFrom lists, for the function at id, each other changed function whose
// nearest route passes through id, as a " > "-joined path of short names from
// the call inside id down to that function.
func expandFrom(id model.TargetID, fns []Function, names map[model.TargetID]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range fns {
		if f.ID == id || len(f.Routes) == 0 {
			continue
		}
		path := f.Routes[0].Path
		for k, hop := range path {
			if hop != id || k == len(path)-1 {
				continue
			}
			parts := make([]string, 0, len(path)-k-1)
			for _, h := range path[k+1:] {
				parts = append(parts, names[h])
			}
			entry := strings.Join(parts, " > ")
			if !seen[entry] {
				seen[entry] = true
				out = append(out, entry)
			}
			break
		}
	}
	return out
}

// ShortName is the last path element of an id's dotted form — "pkg.Func",
// "pkg.Type.Method" — which is what a reader writes at a call site and what
// the symbol lookup resolves unambiguously within one module.
func ShortName(id model.TargetID) string {
	s := string(id)
	if i := strings.Index(s, "::"); i >= 0 {
		// A workspace alias prefix names the repo, not the function.
		s = s[i+2:]
	}
	if strings.HasPrefix(s, "(") {
		if close := strings.IndexByte(s, ')'); close > 0 {
			recv := strings.TrimPrefix(s[1:close], "*")
			s = recv + s[close+1:]
		}
	}
	if slash := strings.LastIndexByte(s, '/'); slash >= 0 {
		s = s[slash+1:]
	}
	return s
}

// graph is the engine's call graph read lazily in both directions, with each
// node's neighbours fetched once however many walks cross it.
type graph struct {
	eng       model.Engine
	callerOf  map[model.TargetID][]model.TargetID
	calleesOf map[model.TargetID][]model.TargetID
}

func newGraph(eng model.Engine) *graph {
	return &graph{
		eng:       eng,
		callerOf:  map[model.TargetID][]model.TargetID{},
		calleesOf: map[model.TargetID][]model.TargetID{},
	}
}

// callers are the functions holding a call or interface dispatch to id, in
// usage order, each once.
func (g *graph) callers(id model.TargetID) []model.TargetID {
	if c, ok := g.callerOf[id]; ok {
		return c
	}
	var out []model.TargetID
	seen := map[model.TargetID]bool{}
	us, _ := g.eng.Usages(id)
	for _, u := range us {
		if u.Kind == model.UsageRef || u.Caller == "" || seen[u.Caller] {
			continue
		}
		seen[u.Caller] = true
		out = append(out, u.Caller)
	}
	g.callerOf[id] = out
	return out
}

// callees are the functions id's body calls, in call order: a direct target,
// every candidate of an interface call, every receiver of a fan-out. A value
// reference is skipped, mirroring callers.
func (g *graph) callees(id model.TargetID) []model.TargetID {
	if c, ok := g.calleesOf[id]; ok {
		return c
	}
	var out []model.TargetID
	seen := map[model.TargetID]bool{}
	add := func(t model.TargetID) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if f, err := g.eng.Frame(id); err == nil && f != nil {
		for _, c := range f.Calls {
			if c.Kind == model.KindRef {
				continue
			}
			add(c.TargetID)
			for _, cand := range c.Candidates {
				add(cand.TargetID)
			}
			for _, r := range c.Receivers {
				add(r.TargetID)
			}
		}
	}
	g.calleesOf[id] = out
	return out
}
