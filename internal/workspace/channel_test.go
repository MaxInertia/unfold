package workspace

import (
	"os"
	"path/filepath"
	"strings"
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

func leafServices(ends []model.LeafEnd) []string {
	out := make([]string, 0, len(ends))
	for _, e := range ends {
		out = append(out, e.Service)
	}
	return out
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
	// Named, and — for the services already indexed — located, which costs
	// nothing more: the code is in an index that has already been read.
	if !equalStrings(leafServices(foo.Ends), []string{"publisher", "publisher2"}) {
		t.Errorf("foo-happened publishers: got %v, want both named", leafServices(foo.Ends))
	}
	for _, e := range foo.Ends {
		if !e.Indexed {
			t.Errorf("%s is indexed in this workspace but the end says otherwise", e.Service)
		}
		if e.Title == "" || e.Path == "" {
			t.Errorf("%s: an indexed end should name its code, got %+v", e.Service, e)
		}
	}
	// Every end is described the same way, whether there is one or five.
	if !equalStrings(leafServices(bar.Ends), []string{"publisher"}) {
		t.Fatalf("bar-happened publishers: got %v, want [publisher]", leafServices(bar.Ends))
	}
	if bar.Ends[0].Title != "publishBar" || bar.Ends[0].Path != "publisher/main.go:9" {
		t.Errorf("bar's publisher is %+v, want publishBar at publisher/main.go:9", bar.Ends[0])
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

// A boundary belongs to the call the rule matched, not to every site that
// happens to share its line.
//
// `c.Subscribe(defn, handleFoo)` is two sites: the call, and the handler named
// as a value. Leaves are keyed by file:line — which was unambiguous until a
// reference became a site — so both inherited the boundary and the card
// rendered twice at one registration.
func TestOneBoundaryPerCallNotPerSiteOnTheLine(t *testing.T) {
	w := bothWays(t, "subscriber")

	id, err := w.LookupSymbol("consume")
	if err != nil {
		t.Fatalf("LookupSymbol(consume): %v", err)
	}
	fr, err := w.Frame(id)
	if err != nil {
		t.Fatalf("Frame(consume): %v", err)
	}

	perLine := map[int]int{}
	for _, c := range fr.Calls {
		if c.Leaf == nil {
			continue
		}
		// Two registrations in this function, each on its own line; each must
		// contribute exactly one boundary.
		perLine[c.SpanStart]++
		if c.Kind == model.KindRef {
			t.Errorf("a reference (%s) inherited the call's boundary", c.DisplayName)
		}
	}
	if len(perLine) != 2 {
		t.Errorf("got %d boundaries in consume, want 2 — one per Subscribe", len(perLine))
	}

	// And the handler reference is still a site: losing the leaf must not cost
	// it its own expandability.
	refs := 0
	for _, c := range fr.Calls {
		if c.Kind == model.KindRef {
			refs++
		}
	}
	if refs != 2 {
		t.Errorf("got %d handler references, want 2", refs)
	}
}

// Two calls on one line, and only one of them is the boundary.
//
// `c.With("retries").Subscribe(defn, h)` is a chain: two call sites and a
// reference, all on the same line. A boundary keyed by line landed on every
// one of them, so one registration drew three cards — which is what a
// registration written as a chain looks like in real code.
func TestChainedRegistrationDrawsOneBoundary(t *testing.T) {
	w := bothWays(t, "subscriber2")

	id, err := w.LookupSymbol("consumeToo")
	if err != nil {
		t.Fatalf("LookupSymbol(consumeToo): %v", err)
	}
	fr, err := w.Frame(id)
	if err != nil {
		t.Fatalf("Frame(consumeToo): %v", err)
	}

	var withBoundary []string
	for _, c := range fr.Calls {
		if c.Leaf != nil {
			withBoundary = append(withBoundary, c.DisplayName)
		}
	}
	if len(withBoundary) != 1 {
		t.Errorf("got %d boundaries on one chained registration (%v), want 1 — on the Subscribe",
			len(withBoundary), withBoundary)
	}
	if len(withBoundary) == 1 && !strings.HasSuffix(withBoundary[0], "Subscribe") {
		t.Errorf("the boundary landed on %q, want the Subscribe call", withBoundary[0])
	}
	// The line still holds three sites; they just don't all claim the boundary.
	if len(fr.Calls) < 3 {
		t.Errorf("expected the chain's sites to still be sites, got %d", len(fr.Calls))
	}
}

// What a boundary can say about an end, and when.
//
// For pubsub there is no middle state: nothing declares a subscription, so a
// service is only an end once its code has been read — before that the
// boundary has no ends at all and says so, rather than naming a service it
// cannot know about. Once read, the end arrives complete, because the code
// that made it an end is the same code that locates it.
//
// (The middle state exists for gRPC, where a proto declares an implementer
// without anyone reading it — that is what LeafEnd.Indexed distinguishes.)
func TestPubsubEndAppearsOnlyWhenItsServiceIsRead(t *testing.T) {
	rulesFile := filepath.Join(t.TempDir(), "recognizers.json")
	err := os.WriteFile(rulesFile, []byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":1},
	   "emit":{"role":"outbound","kind":"pubsub.topic","key":"{arg0.ID}"},
	   "leaf":{"expand":false,"label":"→ subscribers","crossRepo":true}},
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
	// Lazy: only the repo we stand in is read.
	w, err := Open(dirs, abs(t, "testdata/pubsub/publisher"), "", ModeLazy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	leafOn := func(symbol string) *model.LeafInfo {
		t.Helper()
		id, err := w.LookupSymbol(symbol)
		if err != nil {
			t.Fatalf("LookupSymbol(%s): %v", symbol, err)
		}
		fr, err := w.Frame(id)
		if err != nil {
			t.Fatalf("Frame(%s): %v", symbol, err)
		}
		for _, c := range fr.Calls {
			if c.Leaf != nil {
				return c.Leaf
			}
		}
		t.Fatalf("no boundary in %s", symbol)
		return nil
	}

	if ends := leafOn("publish").Ends; len(ends) != 0 {
		t.Errorf("a subscriber nobody has read is not a known end; got %+v", ends)
	}

	// Reading it is what makes it one — and it arrives already located, since
	// the index that revealed it is the index that holds its code.
	if err := w.IndexRepo("subscriber"); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}
	ends := leafOn("publish").Ends
	if len(ends) == 0 {
		t.Fatal("after reading the subscriber, it should be an end")
	}
	for _, e := range ends {
		if !e.Indexed {
			t.Errorf("%s was read, so the end must say so: %+v", e.Service, e)
		}
		if e.Title == "" || e.Path == "" {
			t.Errorf("%s: an end from an indexed service should name and locate its code: %+v", e.Service, e)
		}
	}
}
