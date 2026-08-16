package workspace

import (
	"sort"
	"strconv"

	"github.com/MaxInertia/unfold/internal/model"
)

// The call graph joins two relations that already existed but had never been
// composed.
//
// The crossing relation answers, within one service, "serving this entrypoint
// makes these calls". The channel join answers, across services, "this call is
// served over there". Each stops at the boundary the other crosses: the first
// never leaves a repo, the second never says what *caused* the call. Composing
// them gives the walk a reader actually has in mind —
//
//	gateway(main) → inbox ShowThread → conversation GetConversation
//
// — which no existing level can state. L0 knows gateway calls inbox and inbox
// calls conversation; it cannot say that serving ShowThread is the reason, and
// so it cannot chain the two hops into one path.
//
// The composition is done by *identifying* nodes rather than adding an edge
// type: a call whose key some service serves is drawn as an edge straight to
// that service's entrypoint node. Chains are then plain graph reachability,
// and the cross-repo hop needs no special case in the layout, the marking, or
// the UI.

// nodeKey identifies a node before ids are handed out. Ids are assigned after
// sorting, so they stay stable across runs — a graph whose node ids reshuffle
// between loads can't be linked to.
type nodeKey struct {
	service string // "" for an external key nothing here serves
	channel string // declKey(kind, key); "" for a root
	root    bool
}

// CallGraph builds the workspace-wide graph of entrypoints and the calls
// between them. See model.CallGraph.
//
// Only repos already indexed contribute edges, for the same reason PlatformView
// works that way: knowing what a service calls means having read its code.
// Their *entrypoints* are still known from declarations, so a chain that
// arrives at an unindexed service draws the node it lands on and stops there —
// reported through Unindexed rather than passed off as the end of the path.
func (w *Workspace) CallGraph(anchor model.TargetID) (*model.CallGraph, error) {
	g := &model.CallGraph{Nodes: []model.CallGraphNode{}, Edges: []model.CallGraphEdge{}}

	nodes := map[nodeKey]*model.CallGraphNode{}
	// Insertion order is kept only so the later sort has something
	// deterministic to break ties against.
	var order []nodeKey
	// described marks the nodes filled in from a real binding, as opposed to
	// the stubs a call creates when it lands on a service not walked yet.
	// Repos are visited in a fixed order that has nothing to do with who calls
	// whom, so a callee is routinely stubbed before it is described — and
	// without this the fixture's inbox node kept the caller's thin view of it
	// and lost its own visibility, candidates and handler.
	described := map[nodeKey]bool{}

	node := func(k nodeKey) *model.CallGraphNode {
		if n, ok := nodes[k]; ok {
			return n
		}
		n := &model.CallGraphNode{}
		nodes[k] = n
		order = append(order, k)
		return n
	}

	// edge accumulates by (from, to, kind, key): one entrypoint can reach the
	// same API from several call sites, and those are one edge carrying
	// several sites rather than several parallel lines.
	type edgeKey struct {
		from, to  nodeKey
		kind, key string
	}
	edges := map[edgeKey]*model.CallGraphEdge{}
	var edgeOrder []edgeKey

	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded := r.idx, r.loaded
		r.mu.Unlock()
		if !loaded || idx == nil {
			g.Unindexed = append(g.Unindexed, alias)
			continue
		}
		sv, err := idx.ServiceView("")
		if err != nil {
			g.Unindexed = append(g.Unindexed, alias)
			continue
		}

		// Every entrypoint becomes a node whether or not anything calls it.
		// A public route nobody in the workspace reaches is not noise — it is
		// where work enters the platform, which is the first thing this view
		// is for.
		inboundNode := map[string]nodeKey{} // binding id -> node
		allWalked := true
		for _, b := range sv.Inbound {
			if !b.CrossingKnown {
				allWalked = false
			}
			k := nodeKey{service: alias, channel: declKey(b.Kind, b.Key)}
			n := node(k)
			if !described[k] {
				described[k] = true
				*n = model.CallGraphNode{
					Origin:     model.OriginEntrypoint,
					Service:    alias,
					Kind:       b.Kind,
					Key:        b.Key,
					Title:      b.Key,
					Visibility: b.Visibility,
					Confidence: b.Confidence,
					Stale:      b.Stale,
					Target:     model.TargetID(w.qualify(alias, string(b.Target))),
					Candidates: w.qualifyCandidates(alias, b.Candidates),
					Site:       model.TargetID(w.qualify(alias, string(b.Site))),
					SiteTitle:  b.SiteTitle,
					File:       b.File,
					Line:       b.Line,
				}
			}
			// Two registrations of one API are one node, and the more
			// determined answer wins: a second registration that *was* walked
			// makes the pair's outbound set known.
			if b.CrossingKnown {
				n.OutboundKnown = true
			}
			if b.ID != "" {
				inboundNode[b.ID] = k
			}
		}

		// Which entrypoints cause each outbound call — the crossing relation,
		// read from the outbound side.
		causes := map[string][]nodeKey{}
		for _, b := range sv.Inbound {
			k, ok := inboundNode[b.ID]
			if !ok {
				continue
			}
			for _, out := range b.Reaches {
				causes[out] = append(causes[out], k)
			}
		}

		rootKey := nodeKey{service: alias, root: true}
		for _, b := range sv.Outbound {
			from := causes[b.ID]
			if len(from) == 0 {
				// Nothing this service serves causes this call, so it runs
				// from main, init, or a package-level initializer. That is a
				// real origin and the gateway fixture is entirely this shape —
				// dropping it would lose the service that drives the chain.
				//
				// The claim is only as good as the walks behind it: if some
				// entrypoint had no handler to walk from, this call might
				// belong to it instead, and the node says so rather than
				// asserting an origin it didn't establish.
				root := node(rootKey)
				*root = model.CallGraphNode{
					Origin:        model.OriginRoot,
					Service:       alias,
					Title:         r.name + " (main/init)",
					OutboundKnown: allWalked,
				}
				from = []nodeKey{rootKey}
			}

			// Every service serving this key, not the first: a topic with
			// five subscribers delivers to all five, and drawing one edge
			// would say the other four don't receive it.
			ends := w.endsOf(b.Kind, b.Key, model.RoleInbound)
			var targets []nodeKey
			for _, to := range ends {
				targets = append(targets, nodeKey{service: to, channel: declKey(b.Kind, b.Key)})
			}
			if len(targets) == 0 {
				// Nothing here serves it: a third-party API, or a service
				// that simply isn't checked out. Kept as a node because "the
				// chain leaves the workspace here" is the answer, and
				// dropping it would redraw a call to Stripe as a call to
				// nothing.
				targets = []nodeKey{{channel: declKey(b.Kind, b.Key)}}
			}

			for _, t := range targets {
				// A stub for what the call lands on. An indexed service
				// replaces it when its own entrypoint pass runs, whichever
				// order the repos happen to be walked in; a service that is
				// never indexed keeps it, which is exactly the honest picture
				// — the chain arrives there and what happens next is unread.
				if n := node(t); !described[t] {
					n.Origin = model.OriginEntrypoint
					n.Service = t.service
					n.Kind = b.Kind
					n.Key = b.Key
					n.Title = b.Key
					if t.service == "" {
						n.Origin = model.OriginExternal
					}
				}
				for _, f := range from {
					if f == t {
						continue // a service calling its own API through itself
					}
					ek := edgeKey{from: f, to: t, kind: b.Kind, key: b.Key}
					e, ok := edges[ek]
					if !ok {
						e = &model.CallGraphEdge{
							Kind:       b.Kind,
							Key:        b.Key,
							Confidence: b.Confidence,
							Fanout:     len(ends) > 1,
						}
						edges[ek] = e
						edgeOrder = append(edgeOrder, ek)
					}
					e.Sites = append(e.Sites, model.CallGraphSite{
						Site:      model.TargetID(w.qualify(alias, string(b.Site))),
						SiteTitle: b.SiteTitle,
						File:      b.File,
						Line:      b.Line,
					})
				}
			}
		}
	}

	// Ids are assigned after sorting so they describe a stable position in a
	// stable order, which is what makes a link to a node survive a reload.
	sort.SliceStable(order, func(a, b int) bool { return lessNode(nodes, order[a], order[b]) })
	id := map[nodeKey]string{}
	for n, k := range order {
		nodes[k].ID = "c" + strconv.Itoa(n)
		id[k] = nodes[k].ID
	}

	incoming := map[string]bool{}
	sort.SliceStable(edgeOrder, func(a, b int) bool {
		x, y := edgeOrder[a], edgeOrder[b]
		if id[x.from] != id[y.from] {
			return lessNode(nodes, x.from, y.from)
		}
		if x.key != y.key {
			return x.key < y.key
		}
		return lessNode(nodes, x.to, y.to)
	})
	for _, ek := range edgeOrder {
		e := edges[ek]
		e.From, e.To = id[ek.from], id[ek.to]
		incoming[e.To] = true
		g.Edges = append(g.Edges, *e)
	}

	for _, k := range order {
		n := nodes[k]
		// Where work enters the platform: nothing in the workspace reaches
		// it. Roots qualify by construction — no API leads to a main.
		n.Entry = !incoming[n.ID]
		g.Nodes = append(g.Nodes, *n)
	}

	w.markCallGraphAnchor(g, anchor)
	return g, nil
}

// markCallGraphAnchor marks every node from which execution can arrive at the
// anchor, by walking the graph backwards from the entrypoints that reach it.
//
// This is the same question L0 answers with its keyed fixpoint, asked at a
// granularity where it needs no fixpoint at all: once entrypoints are nodes
// and calls are edges, "what leads here" is one reverse traversal, and cycles
// are handled by having visited a node already.
//
// One known hole, inherited from keysReachingAnchor: a service reaching the
// anchor from its own main or init has no inbound key responsible, so its root
// node is not seeded. Marking it anyway would over-mark — the same trap the
// platform level documents — and establishing it properly needs a backwards
// walk this level doesn't run.
func (w *Workspace) markCallGraphAnchor(g *model.CallGraph, anchor model.TargetID) {
	anchorRepo, keys := w.keysReachingAnchor(anchor)
	if anchorRepo == "" {
		return
	}
	g.Anchor = anchor
	if sv, err := w.ServiceViewOf(anchorRepo, anchor); err == nil {
		g.AnchorTitle = sv.AnchorTitle
	}

	byID := map[string]*model.CallGraphNode{}
	for n := range g.Nodes {
		byID[g.Nodes[n].ID] = &g.Nodes[n]
	}
	callers := map[string][]string{}
	for _, e := range g.Edges {
		callers[e.To] = append(callers[e.To], e.From)
	}

	marked := map[string]bool{}
	var queue []string
	for n := range g.Nodes {
		node := &g.Nodes[n]
		if node.Service != anchorRepo || node.Origin != model.OriginEntrypoint {
			continue
		}
		if !keys[declKey(node.Kind, node.Key)] {
			continue
		}
		node.ReachesAnchor = true
		marked[node.ID] = true
		queue = append(queue, node.ID)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, from := range callers[cur] {
			if marked[from] {
				continue
			}
			marked[from] = true
			if n := byID[from]; n != nil {
				n.ReachesAnchor = true
			}
			queue = append(queue, from)
		}
	}
	for n := range g.Edges {
		// An edge is on a path to the anchor when what it lands on leads
		// there. Its source is then marked too, by construction of the walk.
		if marked[g.Edges[n].To] {
			g.Edges[n].ReachesAnchor = true
		}
	}
}

// lessNode orders nodes for display: by service, roots first within one (they
// are where its work starts), then by kind and key. Externals sort last —
// they are the edge of the workspace, not part of any service.
func lessNode(nodes map[nodeKey]*model.CallGraphNode, a, b nodeKey) bool {
	if (a.service == "") != (b.service == "") {
		return b.service == ""
	}
	if a.service != b.service {
		return a.service < b.service
	}
	if a.root != b.root {
		return a.root
	}
	na, nb := nodes[a], nodes[b]
	if na.Kind != nb.Kind {
		return na.Kind < nb.Kind
	}
	return na.Key < nb.Key
}
