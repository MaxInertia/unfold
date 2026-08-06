package indexer

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// loadRoutes indexes the miniature service in testdata/routes.
func loadRoutes(t *testing.T) *Indexer {
	t.Helper()
	dir, err := filepath.Abs("testdata/routes")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return idx
}

func findBinding(bs []model.Binding, kind, key string) *model.Binding {
	for i := range bs {
		if bs[i].Kind == kind && bs[i].Key == key {
			return &bs[i]
		}
	}
	return nil
}

func TestServiceViewBindings(t *testing.T) {
	idx := loadRoutes(t)
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}

	// Service naming falls back to the repo/module directory name.
	if sv.Name != "routes" {
		t.Errorf("service name: got %q, want %q", sv.Name, "routes")
	}
	if sv.Module != "example.com/routes" {
		t.Errorf("module: got %q, want example.com/routes", sv.Module)
	}
	if sv.Anchor != "" {
		t.Errorf("anchor should be empty when none was asked for, got %q", sv.Anchor)
	}

	// Inbound: both routes, including the one whose pattern is a folded
	// constant expression rather than a bare literal.
	if len(sv.Inbound) != 2 {
		t.Fatalf("expected 2 inbound bindings, got %d: %+v", len(sv.Inbound), sv.Inbound)
	}
	orders := findBinding(sv.Inbound, "http.route", "POST /v1/orders")
	if orders == nil {
		t.Fatalf("missing POST /v1/orders route; got %+v", sv.Inbound)
	}
	if orders.TargetTitle != "Server.handleOrders" {
		t.Errorf("orders handler: got %q, want Server.handleOrders", orders.TargetTitle)
	}
	if orders.SiteTitle != "Server.Handler" {
		t.Errorf("orders registration site: got %q, want Server.Handler", orders.SiteTitle)
	}
	if orders.Confidence != model.ConfExact {
		t.Errorf("orders confidence: got %q, want exact", orders.Confidence)
	}
	if orders.Line == 0 || orders.File == "" {
		t.Errorf("orders binding is missing its location: %+v", orders)
	}

	// The handler reached through an http.HandlerFunc conversion still
	// resolves to the underlying method.
	health := findBinding(sv.Inbound, "http.route", "/health")
	if health == nil {
		t.Fatalf("missing /health route; got %+v", sv.Inbound)
	}
	if health.TargetTitle != "Server.handleHealth" {
		t.Errorf("health handler: got %q, want Server.handleHealth", health.TargetTitle)
	}

	// Outbound: the literal-URL client call, keyed on method + path only.
	if len(sv.Outbound) != 1 {
		t.Fatalf("expected 1 outbound binding, got %d: %+v", len(sv.Outbound), sv.Outbound)
	}
	out := sv.Outbound[0]
	if out.Kind != "http.call" || out.Key != "GET /v1/orders" {
		t.Errorf("outbound: got %s %q, want http.call \"GET /v1/orders\"", out.Kind, out.Key)
	}
	if out.SiteTitle != "fetchOrders" {
		t.Errorf("outbound site: got %q, want fetchOrders", out.SiteTitle)
	}
	// Nothing in this repo serves that key, and that's the expected state
	// for a single-repo index — the far end lives in another service.
	if out.Target != "" {
		t.Errorf("outbound binding should have no in-repo target, got %q", out.Target)
	}
}

// TestServiceViewAnchor covers the mechanism behind zoom-out highlighting:
// with an anchor, exactly the entrypoints that transitively reach it are
// flagged, so the view can dim the rest.
func TestServiceViewAnchor(t *testing.T) {
	idx := loadRoutes(t)
	anchor, err := idx.LookupSymbol("(*example.com/routes.Server).validate")
	if err != nil {
		t.Fatalf("LookupSymbol(validate): %v", err)
	}

	sv, err := idx.ServiceView(anchor)
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Anchor != anchor {
		t.Errorf("anchor: got %q, want %q", sv.Anchor, anchor)
	}
	if sv.AnchorTitle != "Server.validate" {
		t.Errorf("anchor title: got %q, want Server.validate", sv.AnchorTitle)
	}

	orders := findBinding(sv.Inbound, "http.route", "POST /v1/orders")
	if orders == nil || !orders.ReachesAnchor {
		t.Errorf("POST /v1/orders should reach Server.validate: %+v", orders)
	}
	health := findBinding(sv.Inbound, "http.route", "/health")
	if health == nil || health.ReachesAnchor {
		t.Errorf("/health should not reach Server.validate: %+v", health)
	}
}

// An anchor that isn't an indexed function (a stale link, a file frame)
// degrades to the plain view instead of failing.
func TestServiceViewUnknownAnchor(t *testing.T) {
	idx := loadRoutes(t)
	sv, err := idx.ServiceView("example.com/routes.doesNotExist")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Anchor != "" {
		t.Errorf("unknown anchor should not be echoed, got %q", sv.Anchor)
	}
	if len(sv.Inbound) != 2 {
		t.Errorf("bindings should still be present, got %d", len(sv.Inbound))
	}
	for _, b := range sv.Inbound {
		if b.ReachesAnchor {
			t.Errorf("no binding should be marked without a valid anchor: %+v", b)
		}
	}
}

// TestServiceViewSelf is the self-hosting check: unfold's own server
// registers its API surface with net/http, so indexing the module should
// surface those routes as its inbound surface.
func TestServiceViewSelf(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Name != "unfold" {
		t.Errorf("service name: got %q, want unfold", sv.Name)
	}
	health := findBinding(sv.Inbound, "http.route", "/api/health")
	if health == nil {
		t.Fatalf("expected /api/health in unfold's own inbound surface; got %+v", sv.Inbound)
	}
	if health.TargetTitle != "Server.handleHealth" {
		t.Errorf("/api/health handler: got %q, want Server.handleHealth", health.TargetTitle)
	}
	if len(sv.Inbound) < 8 {
		t.Errorf("expected unfold to register at least 8 routes, got %d", len(sv.Inbound))
	}
}

// loadDeclared indexes testdata/declared, a service that declares itself in
// microservice.yaml, with protos resolved against testdata/protoroot.
func loadDeclared(t *testing.T, protoRoot string) *Indexer {
	t.Helper()
	dir, err := filepath.Abs("testdata/declared")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if protoRoot != "" {
		abs, err := filepath.Abs(protoRoot)
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		idx.SetProtoRoot(abs)
	}
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return idx
}

// The manifest's declared name beats the repo directory (which is
// "declared", not "conversation").
func TestManifestNameWins(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Name != "conversation" {
		t.Errorf("service name: got %q, want conversation (from microservice.yaml)", sv.Name)
	}
}

// publicRoutes classifies the code-derived surface: a declared route is
// public, anything else the code registers is internal.
func TestPublicRoutesVisibility(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	pub := findBinding(sv.Inbound, "http.route", "GET /v1/conversations")
	if pub == nil {
		t.Fatalf("missing the declared public route; got %+v", sv.Inbound)
	}
	if pub.Visibility != model.VisPublic {
		t.Errorf("visibility: got %q, want public", pub.Visibility)
	}
	if pub.Stale {
		t.Error("a declared route the code registers is not stale")
	}

	internal := findBinding(sv.Inbound, "http.route", "/internal/debug")
	if internal == nil || internal.Visibility != model.VisInternal {
		t.Errorf("code-registered route in neither list should be internal: %+v", internal)
	}
}

// A publicRoutes entry nothing registers is the drift case: shown, but marked
// stale rather than presented as real surface.
func TestDeclaredButUnregisteredRouteIsStale(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	retired := findBinding(sv.Inbound, "http.route", "/v1/retired")
	if retired == nil {
		t.Fatalf("a declared route with no implementation should still appear; got %+v", sv.Inbound)
	}
	if !retired.Stale {
		t.Error("declared-but-unregistered route should be marked stale")
	}
	if retired.Confidence != model.ConfDeclared {
		t.Errorf("confidence: got %q, want declared", retired.Confidence)
	}
}

// Proto-declared RPCs join the inbound surface, linked to their Go
// implementation when the name resolves unambiguously.
func TestProtoSurface(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Warning != "" {
		t.Fatalf("unexpected warning: %s", sv.Warning)
	}

	get := findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation")
	if get == nil {
		t.Fatalf("missing the proto-declared RPC; got %+v", sv.Inbound)
	}
	if get.Confidence != model.ConfDeclared {
		t.Errorf("confidence: got %q, want declared", get.Confidence)
	}
	if get.Visibility != model.VisPlatform {
		t.Errorf("visibility: got %q, want platform", get.Visibility)
	}
	if get.TargetTitle != "Server.GetConversation" {
		t.Errorf("implementation link: got %q, want Server.GetConversation", get.TargetTitle)
	}
	if get.Stale {
		t.Error("an implemented RPC is not stale")
	}

	// Excluded from SDK generation: implemented here, but no other service
	// can call it — internal, not platform.
	purge := findBinding(sv.Inbound, "grpc.method", "conversation.v1.MaintenanceService/Purge")
	if purge == nil {
		t.Fatalf("excluded protos still contribute surface; got %+v", sv.Inbound)
	}
	if purge.Visibility != model.VisInternal {
		t.Errorf("excluded-from-SDK visibility: got %q, want internal", purge.Visibility)
	}
	// Nothing implements Purge in this fixture.
	if !purge.Stale {
		t.Error("a declared RPC with no implementation should be marked stale")
	}
}

// Without --proto-root the declared paths can't resolve, and the view must
// say so rather than showing an empty gRPC surface as though none existed.
func TestProtoRootMissingWarns(t *testing.T) {
	sv, err := loadDeclared(t, "").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Warning == "" {
		t.Fatal("expected a warning when protoPaths are declared but no proto root is set")
	}
	if findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation") != nil {
		t.Error("no gRPC surface should be produced without a proto root")
	}
	// The code-derived surface is unaffected.
	if findBinding(sv.Inbound, "http.route", "GET /v1/conversations") == nil {
		t.Error("HTTP routes should still be found without a proto root")
	}
}

// TestSetProtoRootLive covers picking the proto root in the UI: it applies
// without a re-index, and a bad root replaces the surface with an error
// rather than leaving a stale one from the previous directory.
func TestSetProtoRootLive(t *testing.T) {
	idx := loadDeclared(t, "") // started with no root, as if --proto-root were omitted
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if !sv.NeedsProtoRoot {
		t.Error("a manifest declaring protos with no root should ask for one")
	}

	good, err := filepath.Abs("testdata/protoroot")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if err := idx.SetProtoRoot(good); err != nil {
		t.Fatalf("SetProtoRoot(%q): %v", good, err)
	}
	sv, _ = idx.ServiceView("")
	if sv.NeedsProtoRoot || sv.Warning != "" {
		t.Errorf("a working root should clear the prompt: needs=%v warning=%q", sv.NeedsProtoRoot, sv.Warning)
	}
	if findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation") == nil {
		t.Fatalf("declared surface should appear without a re-index; got %+v", sv.Inbound)
	}
	// The code-derived surface is untouched by the swap.
	if findBinding(sv.Inbound, "http.route", "GET /v1/conversations") == nil {
		t.Error("HTTP routes should survive a proto-root change")
	}

	// A wrong root must not leave the previous surface standing.
	if err := idx.SetProtoRoot(t.TempDir()); err == nil {
		t.Error("expected an error for a root that doesn't resolve the declared protos")
	}
	sv, _ = idx.ServiceView("")
	if findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation") != nil {
		t.Error("a failed root should drop the surface built from the previous one")
	}
	if sv.Warning == "" {
		t.Error("a failed root should be reported")
	}
}

// TestGRPCOutboundAndImplementationMatch covers both ends of a gRPC edge in
// one fixture, because they share a mechanism.
//
// Outbound: the call site says nothing useful, but the generated client's own
// body states the full method name, so the key comes out exact.
//
// Inbound: the same fixture declares a *second* method called GetConversation
// (the client) plus a generated Unimplemented stub. That's what a real
// package set looks like, and naive name matching would find several
// candidates and give up — reporting an implemented RPC as unimplemented.
func TestGRPCOutboundAndImplementationMatch(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}

	out := findBinding(sv.Outbound, "grpc.method", "conversation.v1.ConversationService/GetConversation")
	if out == nil {
		t.Fatalf("expected an outbound gRPC binding from the client call; got %+v", sv.Outbound)
	}
	if out.Confidence != model.ConfExact {
		t.Errorf("the key is a literal in the client's body, so it's exact; got %q", out.Confidence)
	}
	if out.SiteTitle != "Server.fetchConversation" {
		t.Errorf("outbound site: got %q, want Server.fetchConversation", out.SiteTitle)
	}

	in := findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation")
	if in == nil {
		t.Fatal("missing the declared inbound RPC")
	}
	if in.Stale {
		t.Error("the RPC has an implementation, so it must not be reported stale")
	}
	if in.TargetTitle != "Server.GetConversation" {
		t.Errorf("implementation: got %q, want Server.GetConversation "+
			"(the client method and Unimplemented stub must be filtered out)", in.TargetTitle)
	}
}

// A gRPC edge must be attributed to the call site that makes it, not to every
// caller above it: invokePath looks through wrappers, so without this main()
// would appear to call the RPC too.
func TestGRPCEdgeIsNotRelayedToCallers(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var sites []string
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" && b.Key == "conversation.v1.ConversationService/GetConversation" {
			sites = append(sites, b.SiteTitle)
		}
	}
	if len(sites) != 1 {
		t.Fatalf("expected exactly one outbound site for the RPC, got %v", sites)
	}
	if sites[0] != "Server.fetchConversation" {
		t.Errorf("site: got %q, want the innermost caller Server.fetchConversation", sites[0])
	}
}

// TestGRPCThroughAHandWrittenSDK is the shape that actually occurs: the RPC
// name appears nowhere in the caller, and is several hops inside a dependency
// SDK — reached partly by a plain function call rather than a method call.
//
// Both of those defeated the first implementation. It followed only selector
// callees, so a `invokeGetMulti(...)` hop ended the search; and it shared one
// depth counter across the whole recursion, so a function first reached near
// the limit cached "no path" permanently and poisoned every later lookup.
func TestGRPCThroughAHandWrittenSDK(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	// Two functions here call GetMulti, and both are real edges — so assert
	// over the set rather than picking "the" binding, which map-order made a
	// coin flip.
	var sites []string
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" && b.Key == "accountgroup.v1.AccountGroupService/GetMulti" {
			sites = append(sites, b.SiteTitle)
			if b.Confidence != model.ConfExact {
				t.Errorf("confidence: got %q, want exact", b.Confidence)
			}
		}
	}
	if len(sites) == 0 {
		var keys []string
		for _, o := range sv.Outbound {
			keys = append(keys, o.Kind+" "+o.Key)
		}
		t.Fatalf("the SDK call was not recognized; outbound edges were %v", keys)
	}
	// The SDK is a dependency, so its own internal call sites don't count as
	// this service's surface — the edge belongs to the caller in this repo.
	var found bool
	for _, s := range sites {
		if s == "Server.listAccounts" {
			found = true
		}
		if strings.Contains(s, "Client") {
			t.Errorf("the edge was attributed to the SDK rather than its caller: %q", s)
		}
	}
	if !found {
		t.Errorf("sites: got %v, want Server.listAccounts among them", sites)
	}
}

// A dependency's own platform edges are not this service's. Without that
// restriction, unbounded chain-following buries the real surface under
// library plumbing.
func TestDependencyCallSitesAreNotThisServicesSurface(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	for _, b := range sv.Outbound {
		if strings.Contains(b.File, "testdata/agsdk") {
			t.Errorf("binding attributed to a dependency's internals: %+v", b)
		}
	}
}

// Ownership decides which call sites produce bindings, and it must not depend
// on module metadata: pkg.Module is nil under vendored builds and some go.work
// setups, and testing Module.Main then answered "no" for every function. The
// declared surface comes from protos and kept working, so the symptom was a
// service that appeared to make no outbound calls at all.
func TestOwnershipIsByPathNotModuleMetadata(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")

	// Simulate the failing environment: strip the module metadata that the
	// old test relied on. Path containment must carry it regardless.
	for _, fi := range idx.funcs {
		if fi.pkg != nil {
			fi.pkg.Module = nil
		}
	}
	for _, fi := range idx.funcs {
		if !strings.Contains(idx.fset.Position(fi.decl.Pos()).Filename, "testdata/declared") {
			continue
		}
		if !idx.ownsCode(fi) {
			t.Fatalf("%s is in the indexed project but wasn't recognized as its code", fi.id)
		}
		break
	}
	// A dependency stays a dependency.
	for _, fi := range idx.funcs {
		if strings.Contains(idx.fset.Position(fi.decl.Pos()).Filename, "testdata/agsdk") {
			if idx.ownsCode(fi) {
				t.Errorf("%s is a dependency and must not count as this project's code", fi.id)
			}
			return
		}
	}
	t.Skip("no dependency function found to check the negative case")
}

// Chain-following has to be bounded by distance, not just deduplicated.
// Wiring code, request handlers and DI constructors all transitively reach
// some client, and without a bound every one of them acquired an outbound
// edge — producing RPCs the service never calls.
func TestDistantCallersAreNotOutboundEdges(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	sites := map[string]bool{}
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" {
			sites[b.SiteTitle] = true
		}
	}
	// The call that actually goes through the SDK is an edge.
	if !sites["Server.listAccounts"] {
		t.Errorf("the real SDK call should be an outbound edge; got sites %v", sites)
	}
	// Its distant callers are not.
	for _, far := range []string{"Server.startup", "Server.wireUp", "main"} {
		if sites[far] {
			t.Errorf("%s only reaches the RPC transitively and must not own an edge", far)
		}
	}
}

// Distance alone can't tell an SDK wrapper from program logic: a helper one
// hop away that calls two different RPCs is well inside any sane bound. What
// makes a function a client is that it stands for exactly *one* RPC, so a
// caller of a fan-out helper must not be credited with either.
func TestFanOutHelperIsNotAClient(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	for _, b := range sv.Outbound {
		if b.Kind != "grpc.method" {
			continue
		}
		// refresh() calls syncAll(), which reaches both GetMulti and Create.
		// Neither is refresh's call.
		if b.SiteTitle == "Server.refresh" {
			t.Errorf("a caller of a fan-out helper was credited with %q", b.Key)
		}
	}
	// The direct SDK call is still recognized.
	if findBinding(sv.Outbound, "grpc.method", "accountgroup.v1.AccountGroupService/GetMulti") == nil {
		t.Error("the unambiguous SDK call should still be an outbound edge")
	}
}

// Generating clients in-tree gives a repo one stub method per RPC on the whole
// platform. Those are the *ability* to call, not calls — counting them turned
// the outbound surface into a catalogue of everything callable.
func TestGeneratedClientStubsAreNotOutboundEdges(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	keys := map[string]string{} // key -> site
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" {
			keys[b.Key] = b.SiteTitle
		}
	}

	// The one RPC the service actually calls, attributed to the caller.
	if site, ok := keys["billing.v1.BillingService/Charge"]; !ok {
		t.Errorf("the RPC the service calls is missing; got %v", keys)
	} else if site != "Server.chargeCustomer" {
		t.Errorf("Charge should belong to its caller, got %q", site)
	}

	// Stubs nothing calls contribute nothing, however many exist. (Query is
	// deliberately absent from this list — a handler calls it, which
	// TestOnlyReachableCallSitesAreOutboundEdges covers.)
	for _, uncalled := range []string{
		"billing.v1.BillingService/Refund",
		"inventory.v1.InventoryService/Reserve",
	} {
		if site, ok := keys[uncalled]; ok {
			t.Errorf("%s is only a generated stub (at %q) — the service never calls it", uncalled, site)
		}
	}
}

// A repo can contain clients nothing uses — generated or not, recognized as
// clients or not. Classifying the *client* can't answer whether the service
// calls the RPC; only whether execution reaches the call site can.
func TestOnlyReachableCallSitesAreOutboundEdges(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	sites := map[string]string{}
	for _, b := range sv.Outbound {
		if b.Kind == "grpc.method" {
			sites[b.Key] = b.SiteTitle
		}
	}

	// Reachable from main.
	if _, ok := sites["billing.v1.BillingService/Charge"]; !ok {
		t.Errorf("a call reachable from main is an edge; got %v", sites)
	}
	// Reachable only from a proto-declared gRPC handler. This is what pins
	// the ordering: seed reachability before the declared surface exists and
	// a gRPC-only service loses every outbound edge it has.
	if site, ok := sites["search.v1.SearchService/Query"]; !ok {
		t.Errorf("a call made while serving an RPC is an edge; got %v", sites)
	} else if site != "Server.GetConversation" {
		t.Errorf("Query should belong to the handler making it, got %q", site)
	}
	// Clients sitting in the repo that nothing reaches are not calls.
	for _, unused := range []string{
		"billing.v1.BillingService/Refund",
		"archive.v1.ArchiveService/Purge",
	} {
		if site, ok := sites[unused]; ok {
			t.Errorf("%s is never called (found at %q)", unused, site)
		}
	}
}

// A method path handed to a function that issues no RPC is not a call.
// grpc-gateway's runtime.AnnotateContext takes one as an ordinary argument,
// and an earlier approach — matching any call site passing a path-shaped
// literal — turned every one of those into an outbound edge.
func TestPassingAMethodPathAroundIsNotACall(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	for _, b := range sv.Outbound {
		if b.Key == "ai_assistants.v1.Assistants/ListAssistants" {
			t.Errorf("a path passed to a non-RPC helper became an edge at %q", b.SiteTitle)
		}
	}
}

// Generated code duplicated across packages gives a repo several client types
// per service. One call site calling one RPC is one edge regardless.
func TestDuplicateClientsProduceOneEdgePerCallSite(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var rows []string
	for _, b := range sv.Outbound {
		if b.Key == "dup.v1.DupService/Ping" {
			rows = append(rows, b.SiteTitle)
		}
	}
	if len(rows) != 1 {
		t.Errorf("expected one row for one call site, got %d: %v", len(rows), rows)
	}
}

// A call made only while serving a declared RPC is reachable only if the
// declared surface has been built when reachability is seeded. This pins the
// pass ordering: run the outbound pass first and a gRPC-only service loses
// every edge it has, silently.
func TestDeclaredHandlersSeedReachability(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")

	// The seed set must contain the implementation of a declared RPC.
	reach := idx.entrypointReachable()
	if reach == nil {
		t.Fatal("expected entrypoints to be recognized in this fixture")
	}
	impl, err := idx.LookupSymbol("(*example.com/conversation.Server).GetConversation")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	if !reach[impl] {
		t.Error("the implementation of a declared RPC must be an entrypoint")
	}

	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if findBinding(sv.Outbound, "grpc.method", "search.v1.SearchService/Query") == nil {
		t.Error("a call made while serving an RPC should be an outbound edge")
	}
}

// Call ids were keyed on the call expression's start position, so in a chained
// call the outer call and the inner one shared an id and one silently replaced
// the other — losing a usage, and the caller edge with it. That degraded the
// callers tree generally, not just the platform surface.
func TestChainedCallsAreIndexedSeparately(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")
	// listAccounts is `return agsdk.New().GetMulti(ctx)`: two calls starting
	// at the same token.
	target, err := idx.LookupSymbol("(*example.com/agsdk.Client).GetMulti")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	usages, err := idx.Usages(target)
	if err != nil {
		t.Fatalf("Usages: %v", err)
	}
	var found bool
	for _, u := range usages {
		if u.CallerTitle == "Server.listAccounts" {
			found = true
		}
	}
	if !found {
		var callers []string
		for _, u := range usages {
			callers = append(callers, u.CallerTitle)
		}
		t.Errorf("the chained call's usage was lost; callers were %v", callers)
	}
}

// Excluded call sites are counted so an empty column can be told apart from a
// router unfold can't read. Silence about a dropped surface is the failure
// mode that looks like success.
func TestUnreachableCallSitesAreCounted(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	// The fixture has a function that calls a client but which nothing
	// reaches — a call site execution never arrives at. That's distinct from
	// a stub with no callers, which produces no site to begin with.
	if sv.OutboundUnreachable == 0 {
		t.Error("an unreachable call site should be reported as excluded, not silently dropped")
	}
	for _, b := range sv.Outbound {
		if b.SiteTitle == "DeadPath" {
			t.Errorf("an unreachable call site should not be an edge: %+v", b)
		}
	}
}

// A command's code exists to be run, so a valid main package is an entrypoint
// in its entirety — not just its main function.
//
// Plenty of what a command does is reached in ways the call-graph walk can't
// follow: a cobra RunE closure held in a package-level var, a callback
// registered at init. The fixture's cmd/reporter is exactly that shape, and
// seeding only `func main` left the RPC it calls looking unreachable.
func TestCommandPackagesAreEntrypoints(t *testing.T) {
	sv, err := loadDeclared(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	b := findBinding(sv.Outbound, "grpc.method", "reporting.v1.ReportingService/Export")
	if b == nil {
		var keys []string
		for _, o := range sv.Outbound {
			if o.Kind == "grpc.method" {
				keys = append(keys, o.Key)
			}
		}
		t.Fatalf("an RPC called only from a command should be an outbound edge; got %v", keys)
	}
	if b.SiteTitle == "" {
		t.Errorf("the edge should name its call site: %+v", b)
	}
}

// The anchor asks two questions, and they need opposite walks. Backwards
// answers "what runs this code" and marks entrypoints; forwards answers "what
// does this code run" and marks the calls it makes. Marking outbound rows
// from the backwards closure would state something true — this call is made
// by code that reaches the anchor — but not what the label claims.
func TestAnchorMarksTheCallsItMakes(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")
	anchor, err := idx.LookupSymbol("(*example.com/conversation.Server).chargeCustomer")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	sv, err := idx.ServiceView(anchor)
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if sv.Anchor == "" {
		t.Fatal("anchor was not accepted")
	}

	charge := findBinding(sv.Outbound, "grpc.method", "billing.v1.BillingService/Charge")
	if charge == nil || !charge.ReachedByAnchor {
		t.Errorf("the call the anchor makes should be marked: %+v", charge)
	}
	// A call made elsewhere in the service is not one this anchor makes.
	other := findBinding(sv.Outbound, "grpc.method", "search.v1.SearchService/Query")
	if other != nil && other.ReachedByAnchor {
		t.Errorf("a call on an unrelated path should not be marked: %+v", other)
	}
	// The two directions stay distinct: an outbound row never claims to be an
	// entrypoint reaching the anchor.
	for _, b := range sv.Outbound {
		if b.ReachesAnchor {
			t.Errorf("outbound rows answer the forward question only: %+v", b)
		}
	}
}

// The two columns of the service view describe the same service but neither
// says anything about the other. The crossing relation joins them: hit this
// route, and these are the calls the service makes as a result.
//
// It is not "every call in the service" and not "every call on any path that
// happens to share a helper" — it is forward reachability from *this*
// entrypoint's handler, which is the only reading that answers the question
// someone asks when they click a route.
func TestInboundCrossesToTheCallsItCauses(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}

	byID := map[string]*model.Binding{}
	for n := range sv.Outbound {
		byID[sv.Outbound[n].ID] = &sv.Outbound[n]
	}
	keysOf := func(b *model.Binding) []string {
		var out []string
		for _, id := range b.Reaches {
			if o := byID[id]; o != nil {
				out = append(out, o.Key)
			}
		}
		sort.Strings(out)
		return out
	}

	// GetConversation is a declared RPC whose implementation calls the search
	// service while serving. That call is caused by this entrypoint.
	get := findBinding(sv.Inbound, "grpc.method", "conversation.v1.ConversationService/GetConversation")
	if get == nil {
		t.Fatal("the declared RPC is missing from the inbound surface")
	}
	if !get.CrossingKnown {
		t.Fatal("its implementation is indexed, so the crossing is knowable")
	}
	if got := keysOf(get); len(got) != 1 || got[0] != "search.v1.SearchService/Query" {
		t.Errorf("GetConversation should cause exactly the search call, got %v", got)
	}

	// Every id it names must be an outbound binding — a crossing that points
	// at an inbound row, or at nothing, would render as a phantom.
	for _, id := range get.Reaches {
		if byID[id] == nil {
			t.Errorf("crossing names %q, which is not an outbound binding", id)
		}
	}

	// A handler that calls nothing is a *known* empty, not an unknown one.
	// Collapsing the two would let "unfold can't tell" read as "makes no
	// calls", which is the more confident of the two claims and the wrong one.
	list := findBinding(sv.Inbound, "http.route", "GET /v1/conversations")
	if list == nil {
		t.Fatal("the public route is missing from the inbound surface")
	}
	if !list.CrossingKnown {
		t.Error("its handler is indexed, so reaching nothing is a determined answer")
	}
	if len(list.Reaches) != 0 {
		t.Errorf("listConversations calls nothing, got %v", keysOf(list))
	}

	// A route declared in the manifest that no code registers has no handler
	// at all, so its crossing is genuinely unknown.
	if retired := findBinding(sv.Inbound, "http.route", "/v1/retired"); retired != nil {
		if retired.CrossingKnown || len(retired.Reaches) > 0 {
			t.Errorf("a stale declaration has no handler to walk from: %+v", retired)
		}
	}

	// Ids are unique across the whole surface, since one namespace is what
	// lets the relation name a binding at all.
	seen := map[string]bool{}
	for _, b := range append(append([]model.Binding{}, sv.Inbound...), sv.Outbound...) {
		if b.ID == "" {
			t.Errorf("binding has no id: %+v", b)
		}
		if seen[b.ID] {
			t.Errorf("duplicate binding id %q", b.ID)
		}
		seen[b.ID] = true
	}

	// Outbound rows never carry the relation: it is stored one way and
	// inverted by the reader, so a stored reverse copy would be a second
	// source of truth able to disagree with the first.
	for _, b := range sv.Outbound {
		if len(b.Reaches) > 0 {
			t.Errorf("the relation is stored on inbound only: %+v", b)
		}
	}
}
