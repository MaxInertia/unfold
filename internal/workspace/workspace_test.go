package workspace

import (
	"path/filepath"
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
	if res.Title != "ConversationServer.GetConversation" {
		t.Errorf("title: got %q, want ConversationServer.GetConversation", res.Title)
	}
	if !strings.HasPrefix(string(res.Target), "conversation"+Sep) {
		t.Fatalf("a non-primary target must carry its repo, got %q", res.Target)
	}

	f, err := w.Frame(res.Target)
	if err != nil {
		t.Fatalf("Frame(%q): %v", res.Target, err)
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
