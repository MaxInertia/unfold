package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// bothWays opens the pubsub fixture with a leaf on *both* rules, which is what
// makes a boundary card appear on an emit and on a subscribe. primary selects
// which service the reader is standing in.
func bothWays(t *testing.T, primary string) *Workspace {
	t.Helper()
	rulesFile := filepath.Join(t.TempDir(), "recognizers.json")
	err := os.WriteFile(rulesFile, []byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":1},
	   "emit":{"role":"outbound","kind":"pubsub.topic","key":"{arg0.ID}"},
	   "leaf":{"expand":false,"label":"→ subscribers","crossRepo":true}},
	  {"id":"sdk.subscribe",
	   "match":{"func":"Subscribe","minArgs":2},
	   "emit":{"role":"inbound","kind":"pubsub.subscription","key":"{arg0.ID}","handler":"arg1"},
	   "leaf":{"expand":false,"label":"emitters →","crossRepo":true}}
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
	w, err := Open(dirs, abs(t, "testdata/pubsub/"+primary), "", ModeEager)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.WaitIndexed()
	return w
}

func endServices(ends []model.Endpoint) []string {
	out := make([]string, 0, len(ends))
	for _, e := range ends {
		out = append(out, e.Service)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A topic is not a gRPC method: several services can listen to it, and naming
// the first one found would say the others don't receive it.
func TestEmitResolvesEverySubscriber(t *testing.T) {
	w := bothWays(t, "publisher")

	res, err := w.Resolve("pubsub.topic", "foo-happened", model.RoleOutbound)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := endServices(res.Ends); !equalStrings(got, []string{"subscriber", "subscriber2"}) {
		t.Fatalf("subscribers: got %v, want both, in a fixed order", got)
	}
	for _, e := range res.Ends {
		if e.Role != model.RoleInbound {
			t.Errorf("%s: role %q, want inbound", e.Service, e.Role)
		}
		if !e.Indexed || e.Target == "" || e.Path == "" {
			t.Errorf("%s: end is not openable: %+v", e.Service, e)
		}
		// Every end opens the handler that runs, not the registration.
		if e.Title != "handleFoo" && e.Title != "handleFooAgain" {
			t.Errorf("%s: opens %q, want a handler", e.Service, e.Title)
		}
		if _, err := w.Frame(e.Target); err != nil {
			t.Errorf("%s: target must render: %v", e.Service, err)
		}
	}
}

// The direction the whole session was missing: standing on a subscription and
// asking where the event comes from.
func TestSubscribeResolvesEveryPublisher(t *testing.T) {
	w := bothWays(t, "subscriber")

	res, err := w.Resolve("pubsub.subscription", "foo-happened", model.RoleInbound)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := endServices(res.Ends); !equalStrings(got, []string{"publisher", "publisher2"}) {
		t.Fatalf("publishers: got %v, want both, in a fixed order", got)
	}
	for _, e := range res.Ends {
		if e.Role != model.RoleOutbound {
			t.Errorf("%s: role %q, want outbound", e.Service, e.Role)
		}
		// An outbound end has no handler — what there is to open is the
		// function that makes the call.
		if e.Title != "publish" && e.Title != "publishAgain" {
			t.Errorf("%s: opens %q, want the emitting function", e.Service, e.Title)
		}
		if e.Path == "" {
			t.Errorf("%s: no location", e.Service)
		}
		if _, err := w.Frame(e.Target); err != nil {
			t.Errorf("%s: target must render: %v", e.Service, err)
		}
	}
}

// The subscribe side gets a boundary card of its own, pointing the other way.
func TestSubscribeLeafPointsAtThePublishers(t *testing.T) {
	w := bothWays(t, "subscriber")

	id, err := w.LookupSymbol("consume")
	if err != nil {
		t.Fatalf("LookupSymbol(consume): %v", err)
	}
	fr, err := w.Frame(id)
	if err != nil {
		t.Fatalf("Frame(consume): %v", err)
	}
	var foo, bar *model.LeafInfo
	for n := range fr.Calls {
		leaf := fr.Calls[n].Leaf
		switch {
		case leaf == nil:
		case leaf.Key == "foo-happened":
			foo = leaf
		case leaf.Key == "bar-happened":
			bar = leaf
		}
	}
	if foo == nil || bar == nil {
		t.Fatalf("the Subscribe sites carry no leaves (%+v)", fr.Calls)
	}

	// Role is what makes the direction right: an inbound site leads to
	// publishers. Without it this resolved to subscribers — itself.
	if foo.Role != model.RoleInbound {
		t.Errorf("subscribe leaf role %q, want inbound", foo.Role)
	}
	if foo.Ends != 2 {
		t.Errorf("foo-happened has %d publishers, want 2", foo.Ends)
	}
	// With several ends, the single-end fields stay empty rather than naming
	// one of them as though it were the answer.
	if foo.Service != "" || foo.TargetTitle != "" || foo.TargetPath != "" {
		t.Errorf("a boundary with 2 ends named one anyway: %+v", foo)
	}
	// bar-happened has exactly one publisher, so it does name it.
	if bar.Ends != 1 {
		t.Fatalf("bar-happened has %d publishers, want 1", bar.Ends)
	}
	if bar.Service != "publisher" || bar.TargetTitle != "publishBar" {
		t.Errorf("bar's publisher is %s/%s, want publisher/publishBar", bar.Service, bar.TargetTitle)
	}
	if bar.TargetPath != "publisher/main.go:9" {
		t.Errorf("bar's publisher path %q, want publisher/main.go:9", bar.TargetPath)
	}
}

// The platform graph draws one edge per subscriber, for the same reason
// resolve returns them all.
func TestPlatformDrawsAnEdgePerSubscriber(t *testing.T) {
	w := bothWays(t, "publisher")

	pv, err := w.PlatformView("")
	if err != nil {
		t.Fatalf("PlatformView: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range pv.Edges {
		seen[e.From+"→"+e.To] = true
	}
	for _, want := range []string{
		"publisher→subscriber",
		"publisher→subscriber2",
		"publisher2→subscriber",
		"publisher2→subscriber2",
	} {
		if !seen[want] {
			t.Errorf("no %s edge; got %v", want, seen)
		}
	}
}

// A key with ends on one side only resolves in the direction that has them,
// and says so in the other — rather than reporting an empty answer as though
// the key were unknown.
func TestOneSidedChannelSaysWhichSideIsMissing(t *testing.T) {
	w := bothWays(t, "publisher")

	if _, err := w.Resolve("pubsub.topic", "never-subscribed", model.RoleOutbound); err == nil {
		t.Error("expected an error for a key nothing subscribes to")
	} else if !contains(err.Error(), "inbound") {
		t.Errorf("error %q should name the side that is missing", err)
	}
	if _, err := w.Resolve("pubsub.subscription", "never-emitted", model.RoleInbound); err == nil {
		t.Error("expected an error for a key nothing emits")
	} else if !contains(err.Error(), "outbound") {
		t.Errorf("error %q should name the side that is missing", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
