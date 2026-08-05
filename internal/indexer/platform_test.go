package indexer

import (
	"path/filepath"
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
