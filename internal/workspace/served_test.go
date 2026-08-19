package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// openNoProtos opens the same workspace with nothing declared: no proto root,
// so the only thing that can say which service serves a key is the code.
func openNoProtos(t *testing.T) *Workspace {
	t.Helper()
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()
	return w
}

// The failure this exists for, in full: two repositories indexed, the
// implementation sitting in one of them, and a call site in the other that
// resolved to nothing — because the only thing that ever published "this repo
// serves this key" was a manifest pointing at a shared proto repository.
//
// Nothing here declares anything. The join is made by the two ends of the
// generated code naming the same RPC.
func TestTheServerIsFoundWithoutAnyDeclaration(t *testing.T) {
	w := openNoProtos(t)

	key := "conversation.v1.ConversationService/GetConversation"
	ends := w.endsOf("grpc.method", key, model.RoleInbound)
	if len(ends) != 1 || ends[0] != "conversation" {
		t.Fatalf("conversation should be the inbound end of %s, got %v", key, ends)
	}
	res, err := w.Resolve("grpc.method", key, model.RoleOutbound)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Target != "conversation::(*example.com/conversation.ConversationServer).GetConversation" {
		t.Errorf("the call should resolve to the registered implementation, got %q", res.Target)
	}
}

// And the same fact where a reader actually stands: on the call, in the
// handler that makes it. The boundary names the far side and, because that
// repository is indexed, the code at it — which is what "splice the
// implementation in here" needs.
func TestTheCallSiteOffersTheFarSideWithoutAnyDeclaration(t *testing.T) {
	w := openNoProtos(t)

	f, err := w.Frame("(*example.com/inbox.Server).ShowThread")
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	var leaf *model.LeafInfo
	for _, c := range f.Calls {
		if c.Leaf != nil && c.Leaf.Key == "conversation.v1.ConversationService/GetConversation" {
			leaf = c.Leaf
		}
	}
	if leaf == nil {
		t.Fatalf("the call into conversation should be a boundary; calls: %+v", f.Calls)
	}
	if !leaf.CrossRepo || leaf.Role != model.RoleOutbound {
		t.Errorf("the boundary leads out of this repo, from the outbound end: %+v", leaf)
	}
	if len(leaf.Ends) != 1 {
		t.Fatalf("one service serves this key, got %+v", leaf.Ends)
	}
	end := leaf.Ends[0]
	if end.Service != "conversation" || !end.Indexed {
		t.Errorf("the far end is conversation, already indexed: %+v", end)
	}
	if end.Title != "ConversationServer.GetConversation" {
		t.Errorf("the far end should name the code that serves it, got %q", end.Title)
	}
}

// declaring writes a shared rules file and opens the workspace with it, the
// way an org file is wired in at startup.
func declaring(t *testing.T, mode Mode, body string) *Workspace {
	t.Helper()
	path := filepath.Join(t.TempDir(), "org-recognizers.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := RulePaths
	t.Cleanup(func() { RulePaths = prev })
	RulePaths = []string{path}

	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), "", mode)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return w
}

// The reason a declaration still matters now that registrations are read from
// code: it costs no index. In a lazy workspace the repositories behind the one
// you are standing in have not been read, and until they are, the only thing
// that can say who serves a key is something that was *stated*.
func TestADeclarationAnswersBeforeTheRepoIsIndexed(t *testing.T) {
	w := declaring(t, ModeLazy, `{"serves": [
	  {"service": "conversation", "kind": "grpc.method", "keys": ["conversation.v1.ConversationService/GetConversation"]}
	]}`)

	r := w.repos["conversation"]
	r.mu.Lock()
	loaded := r.loaded
	r.mu.Unlock()
	if loaded {
		t.Fatalf("this test is about an unindexed repo; conversation was indexed")
	}
	ends := w.endsOf("grpc.method", "conversation.v1.ConversationService/GetConversation", model.RoleInbound)
	if len(ends) != 1 || ends[0] != "conversation" {
		t.Fatalf("the declared end should answer without an index, got %v", ends)
	}
}

// A pattern declares a whole service in a line — the shape "point at the
// protos" had, without the protos.
func TestADeclaredPatternCoversEveryKeyUnderIt(t *testing.T) {
	w := declaring(t, ModeLazy, `{"serves": [
	  {"service": "conversation", "kind": "grpc.method", "keys": ["billing.v1.BillingService/*"]}
	]}`)

	ends := w.endsOf("grpc.method", "billing.v1.BillingService/Charge", model.RoleInbound)
	if len(ends) != 1 || ends[0] != "conversation" {
		t.Fatalf("a key under the pattern should resolve to the service that declared it, got %v", ends)
	}
	if ends := w.endsOf("grpc.method", "other.v1.Service/Charge", model.RoleInbound); len(ends) != 0 {
		t.Errorf("a key outside the pattern must not, got %v", ends)
	}
	// And the channel index agrees with the join it indexes, rather than
	// showing a key that resolves as one nobody serves.
	var found bool
	for _, ch := range w.Channels() {
		if ch.Key == "billing.v1.BillingService/*" && len(ch.Inbound) == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("the declared pattern should be a row of its own: %+v", w.Channels())
	}
}

// A declaration about one service says nothing about another, whichever file
// it was written in.
func TestADeclarationBelongsToTheServiceItNames(t *testing.T) {
	w := declaring(t, ModeLazy, `{"serves": [
	  {"service": "gateway", "kind": "http.route", "keys": ["GET /v1/threads"]}
	]}`)
	ends := w.endsOf("http.route", "GET /v1/threads", model.RoleInbound)
	if len(ends) != 1 || ends[0] != "gateway" {
		t.Fatalf("expected gateway to be the end, got %v", ends)
	}
}
