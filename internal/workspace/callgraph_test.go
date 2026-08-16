package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// find returns the node for one service's key, by the identity the graph is
// built on rather than by id — ids are positional and would make every test
// break the moment a fixture grows a service.
func find(t *testing.T, g *model.CallGraph, service, key string) model.CallGraphNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Service == service && n.Key == key {
			return n
		}
	}
	t.Fatalf("no node for %s %q in %d nodes", service, key, len(g.Nodes))
	return model.CallGraphNode{}
}

func rootOf(t *testing.T, g *model.CallGraph, service string) model.CallGraphNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Service == service && n.Origin == model.OriginRoot {
			return n
		}
	}
	t.Fatalf("no root node for %s", service)
	return model.CallGraphNode{}
}

func hasEdge(g *model.CallGraph, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// The whole point of the level: a path that crosses two repos, with the API
// that carries each hop named.
//
// gateway calls inbox and inbox calls conversation, which L0 already knew. What
// it could not say is that *serving ShowThread* is what makes the second call,
// so the two hops never joined into one walk. Here they are one path —
// gateway(main) → ShowThread → GetConversation — and each hop is an edge whose
// call site opens as code.
func TestCallGraphChainsEntrypointsAcrossRepos(t *testing.T) {
	w := open(t, ModeEager)
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}

	gw := rootOf(t, g, "gateway")
	show := find(t, g, "inbox", "inbox.v1.InboxService/ShowThread")
	get := find(t, g, "conversation", "conversation.v1.ConversationService/GetConversation")

	if !hasEdge(g, gw.ID, show.ID) {
		t.Errorf("gateway should reach inbox's ShowThread: %+v", g.Edges)
	}
	if !hasEdge(g, show.ID, get.ID) {
		t.Errorf("serving ShowThread should reach conversation's GetConversation: %+v", g.Edges)
	}

	// The hop into inbox is attributed to gateway's main, not to an
	// entrypoint of its own: gateway serves nothing. Losing that node would
	// lose the service that starts the whole chain.
	if gw.Origin != model.OriginRoot || !gw.Entry {
		t.Errorf("gateway's origin should be a root and an entry: %+v", gw)
	}

	// An edge names where the call is written, which is what makes a line in
	// the graph openable.
	for _, e := range g.Edges {
		if e.From == show.ID && e.To == get.ID {
			if len(e.Sites) != 1 || e.Sites[0].SiteTitle != "Server.ShowThread" {
				t.Errorf("the call site should be inbox's handler: %+v", e.Sites)
			}
		}
	}
}

// A node reached by a call is described by the service that serves it, not by
// the caller that happened to mention it first.
//
// Repos are walked in a fixed order unrelated to who calls whom, so a callee
// is routinely stubbed from a call before its own bindings are read — here
// gateway is visited before inbox. Without the upgrade the node kept the
// caller's thin view and lost inbox's own visibility, handler and proto file,
// which is a silent downgrade: the node still renders, just without anything
// that makes it openable.
func TestCallGraphNodeIsDescribedByItsServer(t *testing.T) {
	w := open(t, ModeEager)
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	show := find(t, g, "inbox", "inbox.v1.InboxService/ShowThread")
	if show.Visibility != model.VisPlatform {
		t.Errorf("visibility should come from inbox's own binding, got %q", show.Visibility)
	}
	if show.Target == "" {
		t.Errorf("the handler should be named so the node opens as code: %+v", show)
	}
	if !show.OutboundKnown {
		t.Error("inbox's handler was walked, so what it calls is known")
	}
}

// Every service serving a key gets an edge. A topic with two subscribers
// delivers to both, and drawing one edge would say the other doesn't receive
// it — the same rule the platform edges follow, at the granularity where you
// can see which handler each copy lands in.
func TestCallGraphFansOutToEverySubscriber(t *testing.T) {
	w := openPubsubWorkspace(t)
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}

	pub := rootOf(t, g, "publisher")
	one := find(t, g, "subscriber", "foo-happened")
	two := find(t, g, "subscriber2", "foo-happened")
	if !hasEdge(g, pub.ID, one.ID) || !hasEdge(g, pub.ID, two.ID) {
		t.Errorf("one publish should reach both subscribers: %+v", g.Edges)
	}
	for _, e := range g.Edges {
		if e.From == pub.ID && e.Key == "foo-happened" && !e.Fanout {
			t.Error("an edge with several receivers should say so")
		}
	}
}

// "Reaches nothing" and "couldn't be walked" are different claims, and the
// distinction has to survive into the graph. A node with no outgoing edges
// otherwise reads as a leaf when it is really an unread page — the more
// confident of the two, asserted from the weaker evidence.
func TestCallGraphKeepsUnknownApart(t *testing.T) {
	w := openPubsubWorkspace(t)
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	// baz-happened is subscribed with an inline closure, so there is no named
	// handler to walk from.
	inline := find(t, g, "subscriber2", "baz-happened")
	if inline.OutboundKnown {
		t.Errorf("an inline handler has nothing to walk from: %+v", inline)
	}
	named := find(t, g, "subscriber2", "foo-happened")
	if !named.OutboundKnown {
		t.Errorf("a named handler was walked: %+v", named)
	}
}

// A call whose key nothing in the workspace serves is still a node. Opening a
// workspace holding only gateway leaves its call to inbox unanswered — and the
// honest picture is a chain that visibly leaves the workspace, not one that
// stops at a caller with no callee.
func TestCallGraphKeepsCallsNothingHereServes(t *testing.T) {
	dir := abs(t, "testdata/ws/gateway")
	w, err := Open([]string{dir}, dir, abs(t, "testdata/protoroot"), ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}

	var ext model.CallGraphNode
	for _, n := range g.Nodes {
		if n.Origin == model.OriginExternal {
			ext = n
		}
	}
	if ext.Key != "inbox.v1.InboxService/ShowThread" {
		t.Fatalf("the unserved call should be kept as an external node: %+v", g.Nodes)
	}
	if ext.Service != "" {
		t.Errorf("nothing here serves it, so it belongs to no service: %+v", ext)
	}
	if !hasEdge(g, rootOf(t, g, "gateway").ID, ext.ID) {
		t.Errorf("the call should still be an edge: %+v", g.Edges)
	}
}

// An entrypoint nothing in the workspace reaches is where work enters the
// platform — the first thing this view is for, and the reason entrypoints are
// nodes whether or not anyone calls them.
func TestCallGraphMarksWhereWorkEnters(t *testing.T) {
	w := open(t, ModeEager)
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	stream := find(t, g, "conversation", "conversation.v1.ConversationService/StreamConversation")
	if !stream.Entry {
		t.Error("an RPC nobody in the workspace calls is an entry point")
	}
	get := find(t, g, "conversation", "conversation.v1.ConversationService/GetConversation")
	if get.Entry {
		t.Error("an RPC inbox calls is not where work enters")
	}
}

// Anchor marking runs the whole chain backwards, across repos.
//
// This is the same answer the platform level computes with a keyed fixpoint,
// and here it needs no fixpoint at all: once entrypoints are nodes, "what
// leads here" is one reverse walk. The finer granularity is the gain — L0 can
// only say gateway reaches the anchor, this says which API of inbox it has to
// go through.
func TestCallGraphAnchorMarkingIsTransitive(t *testing.T) {
	w := open(t, ModeEager)
	anchor, err := w.LookupSymbol("conversation" + Sep + "reachMe")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	g, err := w.CallGraph(anchor)
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	if g.Anchor != anchor {
		t.Errorf("the anchor should be carried up: %q", g.Anchor)
	}

	if !find(t, g, "conversation", "conversation.v1.ConversationService/GetConversation").ReachesAnchor {
		t.Error("the RPC whose implementation reaches the anchor should be marked")
	}
	if !find(t, g, "inbox", "inbox.v1.InboxService/ShowThread").ReachesAnchor {
		t.Error("the RPC one hop upstream should be marked")
	}
	if !rootOf(t, g, "gateway").ReachesAnchor {
		t.Error("a caller two hops upstream should be marked")
	}
	// Marking must stay reachability rather than becoming "everything in the
	// anchor's service".
	if find(t, g, "conversation", "conversation.v1.ConversationService/StreamConversation").ReachesAnchor {
		t.Error("a sibling RPC that does not lead to the anchor must stay unmarked")
	}

	for _, e := range g.Edges {
		if !e.ReachesAnchor {
			t.Errorf("every edge on this fixture leads to the anchor: %+v", e)
		}
	}
}

// The dual, and the one that keeps the marking meaningful: an anchor nothing
// leads to marks nothing. Walking the graph backwards without checking which
// API was actually hit would light every caller in the workspace.
func TestCallGraphAnchorMarkingStaysReachability(t *testing.T) {
	w := open(t, ModeEager)
	anchor, err := w.LookupSymbol("conversation" + Sep + "notCalled")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	g, err := w.CallGraph(anchor)
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.ReachesAnchor {
			t.Errorf("no RPC leads to notCalled, so nothing should be marked: %+v", n)
		}
	}
}

// A chain that arrives at a service nobody has indexed stops there, and the
// graph says which services those are. Otherwise the stop is indistinguishable
// from an end, which is the more confident claim and the wrong one.
func TestCallGraphReportsUnindexedServices(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), abs(t, "testdata/protoroot"), ModeLazy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	if len(g.Unindexed) == 0 {
		t.Fatal("a lazy workspace has services it hasn't read, and should say so")
	}
	// The node inbox's call lands on is still drawn, from the declaration
	// alone — the chain reaches conversation, and only what happens *after*
	// that is unknown.
	get := find(t, g, "conversation", "conversation.v1.ConversationService/GetConversation")
	if get.OutboundKnown {
		t.Errorf("conversation was never read, so what it calls is not known: %+v", get)
	}
}

// Ids are positional, so they have to come from a deterministic order or a
// link to a node stops meaning anything across a reload.
func TestCallGraphIsStableAcrossCalls(t *testing.T) {
	w := open(t, ModeEager)
	first, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	second, err := w.CallGraph("")
	if err != nil {
		t.Fatalf("CallGraph: %v", err)
	}
	if len(first.Nodes) != len(second.Nodes) {
		t.Fatalf("node count changed between calls: %d vs %d", len(first.Nodes), len(second.Nodes))
	}
	for n := range first.Nodes {
		if first.Nodes[n].ID != second.Nodes[n].ID || first.Nodes[n].Key != second.Nodes[n].Key {
			t.Errorf("node %d moved: %+v vs %+v", n, first.Nodes[n], second.Nodes[n])
		}
	}
}

// openPubsubWorkspace builds the publish/subscribe fixture, whose edges exist
// only because two user-defined recognizers key off the event definition. The
// rules are written per-test rather than checked in so the fixture keeps
// working when the built-in rule set changes.
func openPubsubWorkspace(t *testing.T) *Workspace {
	t.Helper()
	rulesFile := filepath.Join(t.TempDir(), "recognizers.json")
	err := os.WriteFile(rulesFile, []byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":1},
	   "emit":{"role":"outbound","kind":"sdk.event","key":"{arg0.ID}"}},
	  {"id":"sdk.subscribe",
	   "match":{"func":"Subscribe","minArgs":2},
	   "emit":{"role":"inbound","kind":"sdk.event","key":"{arg0.ID}","handler":"arg1"}}
	]}`), 0o644)
	if err != nil {
		t.Fatalf("write rules: %v", err)
	}
	prev := RulePaths
	RulePaths = []string{rulesFile}
	t.Cleanup(func() { RulePaths = prev })

	dirs, err := Discover(abs(t, "testdata/pubsub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/pubsub/publisher"), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()
	return w
}
