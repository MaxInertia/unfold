package indexer

import (
	"path/filepath"
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
