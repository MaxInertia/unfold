package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// A pubsub edge across two services, which nothing declares.
//
// The gRPC join works off declarations alone: a proto file says which methods
// a service implements, so "who serves this" is answerable without indexing
// anything. Nothing plays that role for a subscription — only the subscriber's
// own body says it subscribes — so this half of the join fills in as repos are
// indexed. Both ends indexed is the case it exists for.
func TestPubsubEdgeJoinsOnceBothServicesAreIndexed(t *testing.T) {
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

	// The publisher's own view: the emit resolves, and names the service that
	// subscribes — which is only knowable because the subscriber was indexed.
	sv, err := w.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var found bool
	for _, b := range sv.Outbound {
		if b.Kind != "sdk.event" {
			continue
		}
		found = true
		if b.Key != "foo-happened" {
			t.Errorf("emit key: got %q, want foo-happened (the ID field of the definition)", b.Key)
		}
		if b.ServedByRepo != "subscriber" {
			t.Errorf("emit is served by %q, want subscriber", b.ServedByRepo)
		}
	}
	if !found {
		t.Fatalf("no sdk.event outbound binding in the publisher (%d outbound)", len(sv.Outbound))
	}

	// And the hop itself opens the handler in the other repo.
	res, err := w.Resolve("sdk.event", "foo-happened")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Repo != "subscriber" {
		t.Errorf("resolved to repo %q, want subscriber", res.Repo)
	}
	if res.Title != "handleFoo" {
		t.Errorf("resolved to %q (%s), want the handleFoo handler", res.Title, res.Target)
	}

	// The platform graph draws it as an edge between the two services.
	pv, err := w.PlatformView("")
	if err != nil {
		t.Fatalf("PlatformView: %v", err)
	}
	var edge bool
	for _, e := range pv.Edges {
		if e.From == "publisher" && e.To == "subscriber" && e.Kind == "sdk.event" {
			edge = true
		}
	}
	if !edge {
		t.Errorf("no publisher→subscriber sdk.event edge in the platform view (%+v)", pv.Edges)
	}
}

// Reading an emit, you want the handler on the other side — not the SDK's
// marshalling. A `leaf` on the emit rule offers exactly that, and the far end
// is looked up by kind *and* key: a leaf that carried only the key was assumed
// to be gRPC, which is true of the first leaf that existed and of nothing else.
func TestEmitLeafOffersTheSubscribersHandler(t *testing.T) {
	rulesFile := filepath.Join(t.TempDir(), "recognizers.json")
	err := os.WriteFile(rulesFile, []byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":1},
	   "emit":{"role":"outbound","kind":"sdk.event","key":"{arg0.ID}"},
	   "leaf":{"expand":false,"label":"→ subscriber","crossRepo":true}},
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

	id, err := w.LookupSymbol("publish")
	if err != nil {
		t.Fatalf("LookupSymbol(publish): %v", err)
	}
	fr, err := w.Frame(id)
	if err != nil {
		t.Fatalf("Frame(publish): %v", err)
	}

	var leaf *model.LeafInfo
	for _, c := range fr.Calls {
		if c.Leaf != nil {
			leaf = c.Leaf
		}
	}
	if leaf == nil {
		t.Fatalf("the Emit call site carries no leaf (%d sites)", len(fr.Calls))
	}
	if !leaf.CrossRepo {
		t.Error("leaf should offer the far end")
	}
	if leaf.Key != "foo-happened" || leaf.Kind != "sdk.event" {
		t.Fatalf("leaf identifies the far end as %s/%s, want sdk.event/foo-happened", leaf.Kind, leaf.Key)
	}

	// A boundary should say where it goes before anyone clicks it, and say it
	// in terms that name the service — "main.go:11" is true of every repo.
	if leaf.Service != "subscriber" {
		t.Errorf("leaf names service %q, want subscriber", leaf.Service)
	}
	if leaf.TargetTitle != "handleFoo" {
		t.Errorf("leaf names target %q, want handleFoo", leaf.TargetTitle)
	}
	if leaf.TargetPath != "subscriber/main.go:11" {
		t.Errorf("leaf target path %q, want subscriber/main.go:11 — the handler, service-qualified", leaf.TargetPath)
	}
	// The frame carries the same, for its own header.
	if fr.RelPath != "publisher/main.go" {
		t.Errorf("frame relPath %q, want publisher/main.go", fr.RelPath)
	}

	// Which is what the UI hands to resolve, and it has to land on the method
	// the other repo passed to Subscribe.
	res, err := w.Resolve(leaf.Kind, leaf.Key)
	if err != nil {
		t.Fatalf("Resolve(%s, %s): %v", leaf.Kind, leaf.Key, err)
	}
	if res.Title != "handleFoo" {
		t.Errorf("the hop lands on %q, want handleFoo", res.Title)
	}
	if _, err := w.Frame(res.Target); err != nil {
		t.Errorf("the resolved target must render inline: %v", err)
	}
}

// The other direction, and it needs no rule at all: the handler is an argument
// to Subscribe, and a function named as a value is an expandable site.
func TestSubscribeHandlerIsExpandableInPlace(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/pubsub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/pubsub/subscriber"), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()

	id, err := w.LookupSymbol("consume")
	if err != nil {
		t.Fatalf("LookupSymbol(consume): %v", err)
	}
	fr, err := w.Frame(id)
	if err != nil {
		t.Fatalf("Frame(consume): %v", err)
	}
	var ref *model.CallSite
	for n, c := range fr.Calls {
		if c.Kind == model.KindRef {
			ref = &fr.Calls[n]
		}
	}
	if ref == nil {
		t.Fatalf("handleFoo isn't a site in consume (%+v)", fr.Calls)
	}
	body, err := w.FrameForCall(ref.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall(handler ref): %v", err)
	}
	if body.Title != "handleFoo" {
		t.Errorf("expanded %q, want handleFoo", body.Title)
	}
}

// The two ends of a channel are not called the same thing, and the join has to
// survive that. unfold's own vocabulary publishes to a `pubsub.topic` and
// subscribes to a `pubsub.subscription` — different roles, rightly named
// differently — so a join that demanded an identical kind could never connect
// a publish to its subscriber, including for the built-in recognizers.
func TestPubsubJoinsAcrossTopicAndSubscriptionKinds(t *testing.T) {
	rulesFile := filepath.Join(t.TempDir(), "recognizers.json")
	err := os.WriteFile(rulesFile, []byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":1},
	   "emit":{"role":"outbound","kind":"pubsub.topic","key":"{arg0.ID}"},
	   "leaf":{"expand":false,"label":"→ subscriber","crossRepo":true}},
	  {"id":"sdk.subscribe",
	   "match":{"func":"Subscribe","minArgs":2},
	   "emit":{"role":"inbound","kind":"pubsub.subscription","key":"{arg0.ID}","handler":"arg1"}}
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

	// The hop the leaf's "inline" button makes, with the publishing side's
	// kind — which is the only kind that side knows.
	res, err := w.Resolve("pubsub.topic", "foo-happened")
	if err != nil {
		t.Fatalf("Resolve(pubsub.topic): %v", err)
	}
	if res.Title != "handleFoo" {
		t.Errorf("the hop lands on %q, want handleFoo", res.Title)
	}

	sv, err := w.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	for _, b := range sv.Outbound {
		if b.Kind == "pubsub.topic" && b.ServedByRepo != "subscriber" {
			t.Errorf("publish to %q says it's served by %q, want subscriber", b.Key, b.ServedByRepo)
		}
	}

	pv, err := w.PlatformView("")
	if err != nil {
		t.Fatalf("PlatformView: %v", err)
	}
	var edge bool
	for _, e := range pv.Edges {
		if e.From == "publisher" && e.To == "subscriber" {
			edge = true
		}
	}
	if !edge {
		t.Errorf("no publisher→subscriber edge across the two kinds (%+v)", pv.Edges)
	}
}

// A key nothing declares is unknown until its service is indexed, and the two
// states must not be reported as the same thing: "nobody serves this" is a
// claim about the platform, where this is a claim about what has been opened.
func TestUnservedKeyErrorAdmitsIndexing(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/pubsub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/pubsub/publisher"), "", ModeLazy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = w.Resolve("sdk.event", "never-emitted")
	if err == nil {
		t.Fatal("expected an error for a key nothing serves")
	}
	if !strings.Contains(err.Error(), "indexed") {
		t.Errorf("error %q should say the answer depends on what has been indexed", err)
	}
}
