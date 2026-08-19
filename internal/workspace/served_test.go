package workspace

import (
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
