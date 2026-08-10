package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
