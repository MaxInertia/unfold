package workspace

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs %s: %v", p, err)
	}
	return a
}

// open builds the two-repo fixture with inbox as the primary — the service
// making the outbound call.
func open(t *testing.T, mode Mode) *Workspace {
	t.Helper()
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), abs(t, "testdata/protoroot"), mode)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return w
}

func TestDiscover(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 modules, got %v", dirs)
	}
	for _, d := range dirs {
		if base := filepath.Base(d); base != "conversation" && base != "inbox" {
			t.Errorf("unexpected repo %q", d)
		}
	}
}

func TestDiscoverNoModules(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Error("expected an error when a workspace contains no modules")
	}
}

// The join is the whole point: inbox's outbound call and conversation's
// declared proto name the same string, so the edge resolves across repos
// without either side referring to the other.
func TestOutboundResolvesToServingRepo(t *testing.T) {
	w := open(t, ModeEager)
	sv, err := w.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Name != "inbox" {
		t.Fatalf("view should be about the primary repo, got %q", sv.Name)
	}

	var out *model.Binding
	for i := range sv.Outbound {
		if sv.Outbound[i].Kind == "grpc.method" {
			out = &sv.Outbound[i]
		}
	}
	if out == nil {
		t.Fatalf("expected an outbound gRPC edge; got %+v", sv.Outbound)
	}
	if out.Key != "conversation.v1.ConversationService/GetConversation" {
		t.Errorf("key: got %q", out.Key)
	}
	if out.ServedBy != "conversation" || out.ServedByRepo != "conversation" {
		t.Errorf("served by: got %q/%q, want conversation", out.ServedBy, out.ServedByRepo)
	}
	// The local caller stays reachable — that's the other half of the row.
	if out.SiteTitle != "Server.showThread" {
		t.Errorf("caller: got %q, want Server.showThread", out.SiteTitle)
	}
}

// Resolve is the cross-repo hop: it returns a target in another repo's id
// space, which Frame must then be able to open.
func TestResolveOpensTheOtherRepo(t *testing.T) {
	w := open(t, ModeEager)
	res, err := w.Resolve("grpc.method", "conversation.v1.ConversationService/GetConversation")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Service != "conversation" {
		t.Errorf("service: got %q", res.Service)
	}
	// This service is fronted by a decorator, so there is no single
	// implementation — resolution hands back the choices instead of picking
	// one, and either form has to be openable.
	target := res.Target
	if target == "" {
		if len(res.Candidates) == 0 {
			t.Fatalf("resolution produced nothing to open: %+v", res)
		}
		for _, c := range res.Candidates {
			if c.Label == "ConversationServer.GetConversation" {
				target = c.TargetID
			}
		}
		if target == "" {
			t.Fatalf("the real implementation was not among %+v", res.Candidates)
		}
	}
	if !strings.HasPrefix(string(target), "conversation"+Sep) {
		t.Fatalf("a non-primary target must carry its repo, got %q", target)
	}

	f, err := w.Frame(target)
	if err != nil {
		t.Fatalf("Frame(%q): %v", target, err)
	}
	if !strings.Contains(f.Source, "func (s *ConversationServer) GetConversation") {
		t.Errorf("frame is not the implementation:\n%s", f.Source)
	}
	// Ids leaving the workspace stay namespaced, or a follow-up request
	// would be served by the wrong repo.
	if !strings.HasPrefix(string(f.ID), "conversation"+Sep) {
		t.Errorf("frame id lost its repo: %q", f.ID)
	}
}

func TestResolveUnknownKey(t *testing.T) {
	w := open(t, ModeEager)
	if _, err := w.Resolve("grpc.method", "nope.v1.Nope/Nope"); err == nil {
		t.Error("expected an error for a key no repo serves")
	}
}

// Lazy mode must answer "who serves this" without having indexed that repo,
// and index it only when the jump is actually taken. That split is what keeps
// a large workspace usable.
func TestLazyDefersIndexingUntilResolve(t *testing.T) {
	w := open(t, ModeLazy)

	indexed := func(alias string) bool {
		for _, r := range w.Repos() {
			if r.Alias == alias {
				return r.Indexed
			}
		}
		t.Fatalf("no repo %q", alias)
		return false
	}
	if !indexed("inbox") {
		t.Error("the primary repo is always indexed — it's what you're reading")
	}
	if indexed("conversation") {
		t.Fatal("lazy mode should not have indexed the other repo yet")
	}

	sv, err := w.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var served string
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" {
			served = b.ServedBy
		}
	}
	if served != "conversation" {
		t.Errorf("declarations alone should identify the serving service, got %q", served)
	}
	if indexed("conversation") {
		t.Error("naming the serving service must not have cost a Go index")
	}

	if _, err := w.Resolve("grpc.method", "conversation.v1.ConversationService/GetConversation"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !indexed("conversation") {
		t.Error("taking the jump should have indexed the serving repo")
	}
}

// Primary-repo ids stay bare so single-repo URLs and bookmarks keep working
// after a workspace is opened.
func TestPrimaryIdsStayUnprefixed(t *testing.T) {
	w := open(t, ModeEager)
	id, err := w.LookupSymbol("showThread")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	if strings.Contains(string(id), Sep) {
		t.Errorf("primary ids should not be namespaced, got %q", id)
	}
	if _, err := w.Frame(id); err != nil {
		t.Errorf("Frame(%q): %v", id, err)
	}
}

// Search spans indexed repos and labels the ones outside the primary.
func TestSearchSpansIndexedRepos(t *testing.T) {
	w := open(t, ModeEager)
	var foreign int
	for _, r := range w.Search("GetConversation", 25) {
		if strings.Contains(string(r.TargetID), Sep) {
			foreign++
			if !strings.HasPrefix(r.Label, "conversation · ") {
				t.Errorf("a hit outside the primary repo should say which one: %q", r.Label)
			}
		}
	}
	if foreign == 0 {
		t.Error("expected the other repo's implementation among the results")
	}
}

func TestAutoModeEagerBelowLimit(t *testing.T) {
	w := open(t, ModeAuto)
	for _, r := range w.Repos() {
		if !r.Indexed {
			t.Errorf("a %d-repo workspace should index eagerly under auto; %s is not indexed",
				len(w.order), r.Alias)
		}
	}
}

// The platform view lists every service from declarations, but can only draw
// outgoing edges for services whose code has been read. A lazy workspace must
// therefore say which services are unindexed rather than presenting them as
// leaves that call nothing.
func TestPlatformViewEdgesFollowIndexing(t *testing.T) {
	w := open(t, ModeLazy)

	pv, err := w.PlatformView()
	if err != nil {
		t.Fatalf("PlatformView: %v", err)
	}
	if len(pv.Services) != 2 {
		t.Fatalf("every service should be listed from declarations alone, got %+v", pv.Services)
	}
	byAlias := map[string]model.PlatformService{}
	for _, s := range pv.Services {
		byAlias[s.Alias] = s
	}
	if !byAlias["inbox"].Primary || !byAlias["inbox"].Indexed {
		t.Errorf("inbox should be the indexed primary: %+v", byAlias["inbox"])
	}
	if byAlias["conversation"].Indexed {
		t.Error("conversation should not be indexed yet in lazy mode")
	}
	// conversation's declared surface is known without its code.
	if byAlias["conversation"].Methods == 0 {
		t.Error("declared RPC count should come from protos, not from indexing")
	}

	if len(pv.Edges) != 1 {
		t.Fatalf("expected the inbox→conversation edge, got %+v", pv.Edges)
	}
	e := pv.Edges[0]
	if e.From != "inbox" || e.To != "conversation" || e.Kind != "grpc.method" {
		t.Errorf("edge: got %s→%s (%s)", e.From, e.To, e.Kind)
	}
	if len(e.Calls) != 1 || e.Calls[0].SiteTitle != "Server.showThread" {
		t.Errorf("edge should carry its call site: %+v", e.Calls)
	}

	// Indexing the other service can only add edges, never remove them.
	if err := w.IndexRepo("conversation"); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}
	pv2, err := w.PlatformView()
	if err != nil {
		t.Fatalf("PlatformView: %v", err)
	}
	if len(pv2.Edges) < len(pv.Edges) {
		t.Errorf("edges shrank after indexing: %d then %d", len(pv.Edges), len(pv2.Edges))
	}
	for _, s := range pv2.Services {
		if !s.Indexed {
			t.Errorf("%s should be indexed now", s.Alias)
		}
	}
}

// A real service fronts its gRPC surface with decorators and ships generated
// mocks beside the implementation, so several methods carry each RPC's name.
// Discarding the link in that case left the row unopenable *and* permanently
// unreachable, because reachability was computed from the very fields the
// discard left empty.
func TestAmbiguousImplementationsAreEnumeratedNotDropped(t *testing.T) {
	w := open(t, ModeEager)
	sv, err := w.ServiceViewOf("conversation", "")
	if err != nil {
		t.Fatalf("ServiceViewOf: %v", err)
	}
	var b *model.Binding
	for i := range sv.Inbound {
		if sv.Inbound[i].Key == "conversation.v1.ConversationService/GetConversation" {
			b = &sv.Inbound[i]
		}
	}
	if b == nil {
		t.Fatalf("missing the declared RPC; got %+v", sv.Inbound)
	}
	if b.Stale {
		t.Error("several implementations is not the same as none — not stale")
	}
	if b.Target != "" {
		t.Fatalf("with a decorator present there is no single implementation, got %q", b.Target)
	}
	if len(b.Candidates) < 2 {
		t.Fatalf("expected the implementations to be enumerated, got %+v", b.Candidates)
	}
	// The generated mock is a double, never an answer.
	for _, c := range b.Candidates {
		if strings.Contains(c.Label, "Mock") {
			t.Errorf("a generated mock should not be offered as an implementation: %q", c.Label)
		}
	}
	// Both the real server and the decorator are real answers.
	labels := map[string]bool{}
	for _, c := range b.Candidates {
		labels[c.Label] = true
	}
	if !labels["ConversationServer.GetConversation"] || !labels["loggingServer.GetConversation"] {
		t.Errorf("expected both the implementation and its decorator, got %v", labels)
	}
}

// An enumerated binding must still be markable as reaching the anchor: an RPC
// with three possible implementations is an entrypoint if any of them leads
// there.
func TestEnumeratedBindingStillReachesTheAnchor(t *testing.T) {
	w := open(t, ModeEager)
	anchor, err := w.LookupSymbol("conversation" + Sep + "reachMe")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	sv, err := w.ServiceViewOf("conversation", anchor)
	if err != nil {
		t.Fatalf("ServiceViewOf: %v", err)
	}
	for _, b := range sv.Inbound {
		if b.Key != "conversation.v1.ConversationService/GetConversation" {
			continue
		}
		if len(b.Candidates) == 0 {
			t.Fatal("expected an enumerated binding for this test to mean anything")
		}
		if !b.ReachesAnchor {
			t.Errorf("a candidate reaches the anchor, so the binding does: %+v", b)
		}
		return
	}
	t.Fatal("missing the declared RPC")
}

// The anchor badge means "this entrypoint leads to the anchored frame", which
// only makes sense inbound: the closure runs backwards. An outbound row
// labelled from that same closure states something true — this call is made
// by code that reaches the anchor — but not what the badge claims.
func TestOnlyInboundBindingsCarryTheAnchorBadge(t *testing.T) {
	w := open(t, ModeEager)
	anchor, err := w.LookupSymbol("showThread")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	sv, err := w.ServiceView(anchor)
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Anchor == "" {
		t.Fatal("anchor was not accepted; the rest of this test proves nothing")
	}
	for _, b := range sv.Outbound {
		if b.ReachesAnchor {
			t.Errorf("outbound bindings must not carry the anchor badge: %+v", b)
		}
	}
}

// Qualifying ids must not mutate the engine's own data. A Binding is handed
// out by value, but its Candidates slice header still points at the array the
// indexer stored — so rewriting in place prefixed that array, and prefixed it
// again on every later request until the ids matched nothing and the anchor
// badge silently stopped working.
//
// Every other test here makes a single call, which is exactly why none of
// them caught it. This one calls twice.
func TestRepeatedViewsDoNotCorruptIds(t *testing.T) {
	w := open(t, ModeEager)
	anchor, err := w.LookupSymbol("conversation" + Sep + "reachMe")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}

	var first []model.Candidate
	for call := 1; call <= 3; call++ {
		sv, err := w.ServiceViewOf("conversation", anchor)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		var b *model.Binding
		for i := range sv.Inbound {
			if len(sv.Inbound[i].Candidates) > 0 {
				b = &sv.Inbound[i]
			}
		}
		if b == nil {
			t.Fatalf("call %d: expected an enumerated binding", call)
		}
		for _, c := range b.Candidates {
			if strings.Count(string(c.TargetID), Sep) != 1 {
				t.Fatalf("call %d: id was qualified more than once: %q", call, c.TargetID)
			}
		}
		if call == 1 {
			first = b.Candidates
			continue
		}
		if !reflect.DeepEqual(b.Candidates, first) {
			t.Fatalf("call %d returned different ids than call 1:\n got %+v\nwant %+v",
				call, b.Candidates, first)
		}
		// The badge depends on those ids matching the reachability closure,
		// so corruption shows up here too.
		if !b.ReachesAnchor {
			t.Errorf("call %d: binding stopped reaching the anchor", call)
		}
	}
}

// The same hazard applies to frames from a non-primary repo.
func TestRepeatedFramesDoNotCorruptIds(t *testing.T) {
	w := open(t, ModeEager)
	id, err := w.LookupSymbol("conversation" + Sep + "GetConversation")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	for call := 1; call <= 3; call++ {
		f, err := w.Frame(id)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		if strings.Count(string(f.ID), Sep) != 1 {
			t.Fatalf("call %d: frame id qualified more than once: %q", call, f.ID)
		}
		for _, c := range f.Calls {
			if strings.Count(string(c.ID), Sep) != 1 {
				t.Fatalf("call %d: call id qualified more than once: %q", call, c.ID)
			}
			for _, cand := range c.Candidates {
				if strings.Count(string(cand.TargetID), Sep) != 1 {
					t.Fatalf("call %d: candidate qualified more than once: %q", call, cand.TargetID)
				}
			}
		}
	}
}

// Launching from inside a repo but below its root is a normal way to run
// this, and it must not fall through to "some other service".
func TestPrimaryResolvesFromASubdirectory(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sub := filepath.Join(abs(t, "testdata/ws/conversation"), "does", "not", "exist")
	w, err := Open(dirs, sub, abs(t, "testdata/protoroot"), ModeLazy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if w.primary != "conversation" {
		t.Errorf("primary: got %q, want conversation (the repo containing the cwd)", w.primary)
	}
}

func TestUnderDirDoesNotMatchSiblingPrefixes(t *testing.T) {
	tests := []struct {
		path, dir string
		want      bool
	}{
		{"/src/orders/internal/a.go", "/src/orders", true},
		{"/src/orders", "/src/orders", true},
		// The hazard: a sibling checkout whose name extends another's.
		{"/src/orders-v2/main.go", "/src/orders", false},
		{"/src/other/main.go", "/src/orders", false},
		{"", "/src/orders", false},
		{"/src/orders/main.go", "", false},
	}
	for _, tt := range tests {
		if got := underDir(tt.path, tt.dir); got != tt.want {
			t.Errorf("underDir(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
		}
	}
}
