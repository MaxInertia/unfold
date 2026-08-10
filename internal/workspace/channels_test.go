package workspace

import (
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

func channelsByKey(cs []model.Channel) map[string]model.Channel {
	m := make(map[string]model.Channel, len(cs))
	for _, c := range cs {
		m[c.Key] = c
	}
	return m
}

func servicesOf(ends []model.ChannelEnd) []string {
	out := make([]string, 0, len(ends))
	for _, e := range ends {
		out = append(out, e.Service)
	}
	return out
}

// The index behind "what events are there, and who is on them". It answers
// from the join alone, so it describes the whole workspace without opening a
// single frame.
func TestChannelsListsBothSidesOfEveryKey(t *testing.T) {
	w := bothWays(t, "publisher")

	byKey := channelsByKey(w.Channels())
	foo, ok := byKey["foo-happened"]
	if !ok {
		t.Fatalf("foo-happened missing; got %v", byKey)
	}
	if got := servicesOf(foo.Outbound); !equalStrings(got, []string{"publisher", "publisher2"}) {
		t.Errorf("foo publishers: got %v, want both", got)
	}
	if got := servicesOf(foo.Inbound); !equalStrings(got, []string{"subscriber", "subscriber2"}) {
		t.Errorf("foo subscribers: got %v, want both", got)
	}

	// The singular case sits in the same list without being a special shape.
	bar, ok := byKey["bar-happened"]
	if !ok {
		t.Fatalf("bar-happened missing; got %v", byKey)
	}
	if got := servicesOf(bar.Outbound); !equalStrings(got, []string{"publisher"}) {
		t.Errorf("bar publishers: got %v", got)
	}
	if got := servicesOf(bar.Inbound); !equalStrings(got, []string{"subscriber"}) {
		t.Errorf("bar subscribers: got %v", got)
	}
	for _, e := range append(append([]model.ChannelEnd{}, bar.Inbound...), bar.Outbound...) {
		if !e.Indexed {
			t.Errorf("%s should be marked indexed in an eager workspace", e.Service)
		}
		if e.Repo == "" {
			t.Errorf("%s has no repo alias to navigate with", e.Service)
		}
	}
}

// Order is fixed, because a list that reshuffles between runs is one nobody
// can talk about — and the order here is whichever background load finished
// first unless it's imposed.
func TestChannelsAreOrdered(t *testing.T) {
	w := bothWays(t, "publisher")

	cs := w.Channels()
	for n := 1; n < len(cs); n++ {
		if cs[n-1].Key > cs[n].Key {
			t.Errorf("channels out of order: %q before %q", cs[n-1].Key, cs[n].Key)
		}
	}
	// Twice in a row must agree, which map iteration alone would not.
	again := w.Channels()
	if len(again) != len(cs) {
		t.Fatalf("two reads disagree: %d then %d", len(cs), len(again))
	}
	for n := range cs {
		if cs[n].Key != again[n].Key {
			t.Errorf("read %d: %q then %q", n, cs[n].Key, again[n].Key)
		}
	}
}

// A key with nothing on one side is kept. A topic nobody subscribes to is
// exactly what someone opens this list to find, and dropping it would make the
// list disagree with the platform it describes.
func TestChannelsKeepOneSidedKeys(t *testing.T) {
	w := bothWays(t, "publisher")
	w.publish("pubsub.topic", "nobody-listens", "publisher", model.RoleOutbound)

	byKey := channelsByKey(w.Channels())
	c, ok := byKey["nobody-listens"]
	if !ok {
		t.Fatalf("a topic with no subscriber was dropped from the index")
	}
	if len(c.Inbound) != 0 {
		t.Errorf("inbound: got %v, want none", servicesOf(c.Inbound))
	}
	if got := servicesOf(c.Outbound); !equalStrings(got, []string{"publisher"}) {
		t.Errorf("outbound: got %v, want publisher", got)
	}
}

// The declared half is listed before any Go is read: that is the whole point
// of the declaration layer, and the index has to inherit it.
func TestChannelsIncludeDeclaredEndsOfUnindexedServices(t *testing.T) {
	dirs, err := Discover(abs(t, "testdata/ws"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	w, err := Open(dirs, abs(t, "testdata/ws/inbox"), abs(t, "testdata/protoroot"), ModeLazy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var declared *model.Channel
	for _, c := range w.Channels() {
		if c.Channel == "grpc.method" && len(c.Inbound) > 0 {
			declared = &c
			break
		}
	}
	if declared == nil {
		t.Fatal("no declared gRPC method in the index")
	}
	// Lazy: conversation's code hasn't been read, and the end says so rather
	// than implying there's something to open.
	for _, e := range declared.Inbound {
		if e.Service == "conversation" && e.Indexed {
			t.Errorf("%s is not indexed in a lazy workspace but claims to be", e.Service)
		}
	}
}
