package indexer

import (
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

func loadServed(t *testing.T, protoRoot string) *Indexer {
	t.Helper()
	dir, err := filepath.Abs("testdata/served")
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

// The point of the pass: a repository with no manifest and no proto root still
// says which RPCs it serves, because its own registration says so. Before this,
// the surface of such a service was empty and every cross-repo call into it
// resolved to nothing.
func TestRegistrationIsTheServedSurface(t *testing.T) {
	sv, err := loadServed(t, "").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	add := findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/AddNote")
	if add == nil {
		t.Fatalf("the registered RPC should be inbound surface; got %+v", sv.Inbound)
	}
	if add.Target != "(*example.com/served.NotesServer).AddNote" {
		t.Errorf("the RPC should open as the implementation registered, got %q", add.Target)
	}
	if add.Confidence != model.ConfExact {
		t.Errorf("a key read from generated code is exact, got %q", add.Confidence)
	}
	// A streaming RPC is registered in a second list on the descriptor and is
	// no less served for it.
	if findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/WatchNotes") == nil {
		t.Errorf("a streaming RPC should be surface too; got %+v", sv.Inbound)
	}
}

// The generated helper hands its own parameter to RegisterService, so its body
// is shaped exactly like a registration. Reading it as one would describe every
// service twice and name no implementation either time.
func TestGeneratedRegistrationHelperIsNotItsOwnRegistration(t *testing.T) {
	sv, err := loadServed(t, "").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var rows []model.Binding
	for _, b := range sv.Inbound {
		if b.Key == "notes.v1.NotesService/AddNote" {
			rows = append(rows, b)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("one registration is one row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Site == "example.com/served.RegisterNotesServiceServer" {
		t.Errorf("the registration belongs to the caller, not to the generated helper")
	}
}

// The declared tier knows things this one cannot — which proto file an RPC is
// declared in, and whether it is excluded from SDK generation — so a repo that
// has a manifest reads exactly as it did before this pass existed.
func TestDeclaredSurfaceWinsOverTheRegistration(t *testing.T) {
	sv, err := loadServed(t, "testdata/protoroot").ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var rows []model.Binding
	for _, b := range sv.Inbound {
		if b.Key == "conversation.v1.ConversationService/GetConversation" {
			rows = append(rows, b)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("the same RPC known from two tiers is one row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Confidence != model.ConfDeclared {
		t.Errorf("the declared tier should describe it, got %q", rows[0].Confidence)
	}
	if rows[0].Detail != "conversation/v1/api.proto" {
		t.Errorf("the proto file is what the declared tier adds, got %q", rows[0].Detail)
	}
	// The excluded service keeps the visibility only the manifest knows: it is
	// implemented here and no other service can call it.
	purge := findBinding(sv.Inbound, "grpc.method", "conversation.v1.MaintenanceService/Purge")
	if purge == nil {
		t.Fatalf("the excluded RPC should still be surface; got %+v", sv.Inbound)
	}
	if purge.Visibility != model.VisInternal {
		t.Errorf("an SDK-excluded RPC is internal, got %q", purge.Visibility)
	}
	// The RPC the manifest says nothing about is still read from the code, so
	// the two tiers add up rather than one replacing the other.
	if findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/AddNote") == nil {
		t.Errorf("a registered RPC outside the manifest is still surface; got %+v", sv.Inbound)
	}
}

// A registered RPC is an entrypoint, which is what makes the code it reaches
// reachable. Without that, an outbound call made only while serving one looks
// like a call the service never makes.
func TestRegisteredRPCsSeedReachability(t *testing.T) {
	idx := loadServed(t, "")
	reach := idx.entrypointReachable()
	if reach == nil {
		t.Fatalf("a service with registrations has recognized entrypoints")
	}
	if !reach["(*example.com/served.NotesServer).store"] {
		t.Errorf("code reached only by serving a registered RPC should be reachable")
	}
}

// Switching the pass off has to empty the surface it produces, or "turn that
// rule off" is a claim the tool doesn't honour.
func TestServedSurfaceCanBeSwitchedOff(t *testing.T) {
	dir, err := filepath.Abs("testdata/served")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	off := false
	idx.SetRules(rules.Set{Rules: []rules.Rule{{ID: servedRuleID, Enabled: &off}}})
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if b := findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/AddNote"); b != nil {
		t.Errorf("a disabled pass must contribute nothing, got %+v", b)
	}
}

// The other half of the same gap: a call site recognized by the built-in
// outbound pass carries no boundary, so the code that makes a cross-service
// call showed a call into a generated stub with nothing saying the
// implementation is one hop away. Every boundary in the UI hangs off this
// field, and until now only a configured rule could set it.
func TestGRPCCallSitesCarryABoundary(t *testing.T) {
	idx := loadDeclared(t, "testdata/protoroot")
	f, err := idx.Frame("(*example.com/conversation.Server).fetchConversation")
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	var leaf *model.LeafInfo
	for _, c := range f.Calls {
		if c.TargetID == "(*example.com/conversation.conversationServiceClient).GetConversation" {
			leaf = c.Leaf
		}
	}
	if leaf == nil {
		t.Fatalf("the call through the generated client should be a boundary; calls: %+v", f.Calls)
	}
	if !leaf.CrossRepo {
		t.Errorf("the boundary leads to another repository: %+v", leaf)
	}
	if leaf.Key != "conversation.v1.ConversationService/GetConversation" || leaf.Kind != "grpc.method" {
		t.Errorf("the boundary carries the key the far side is found by, got %+v", leaf)
	}
	if leaf.Role != model.RoleOutbound {
		t.Errorf("the call site is the outbound end, got %q", leaf.Role)
	}
}

// A rule someone wrote about a call site is the last word about it: the
// built-in fills in where no rule claimed the site, and never over it.
func TestAConfiguredLeafWinsOverTheBuiltinBoundary(t *testing.T) {
	dir, err := filepath.Abs("testdata/declared")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	idx.SetRules(rules.Set{Rules: []rules.Rule{{
		ID:    "mine.conversation",
		Match: &rules.Match{Recv: "conversationServiceClient", Func: "GetConversation"},
		Leaf:  &rules.Leaf{Label: "→ conversation"},
	}}})
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	f, err := idx.Frame("(*example.com/conversation.Server).fetchConversation")
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	for _, c := range f.Calls {
		if c.TargetID != "(*example.com/conversation.conversationServiceClient).GetConversation" || c.Leaf == nil {
			continue
		}
		if c.Leaf.Rule != "mine.conversation" || c.Leaf.Label != "→ conversation" {
			t.Errorf("the configured rule should describe this site, got %+v", c.Leaf)
		}
		return
	}
	t.Fatalf("no leaf on the call site at all")
}
