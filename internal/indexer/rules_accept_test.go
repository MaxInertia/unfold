package indexer

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

// The acceptance test for the rule vocabulary.
//
// If a configured rule set can't express the pattern that was hardest to get
// right — a generated client whose body issues the transport call, attributed
// to the caller that owns it — then the vocabulary won't survive the next
// in-house library either. So the built-in gRPC shape is written as
// configuration and checked against the same fixture the built-in pass is
// pinned to.
//
// What it does NOT assert is that a rule can re-express the *evaluator*.
// Reachability filtering, owned-caller attribution and dedup are engine
// properties applied to every rule's output; this test proves the language can
// state the shape, not that it can restate the machinery.
func TestRulesCanExpressTheBuiltinGRPCShape(t *testing.T) {
	dir, err := filepath.Abs("testdata/declared")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	// The generated clients in this fixture issue c.cc.Invoke(ctx, Method, …)
	// where cc is a conn type declared alongside them. That is the shape — a
	// method-path constant handed to a transport call — and it's stated here
	// without naming any particular RPC.
	set := rules.Load()
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"test.grpc.invoke",
	   "//":"a transport call carrying a method path",
	   "classifier":true,
	   "match":{"recv":"*onn","func":"Invoke","minArgs":2},
	   "emit":{"role":"outbound","kind":"rule.grpc","key":"{arg1}"}},
	  {"id":"test.grpc.client",
	   "//":"a call into something whose own body issues that transport call",
	   "match":{"func":"*","calleeMatches":{"rule":"test.grpc.invoke","depth":1}},
	   "emit":{"role":"outbound","kind":"rule.grpc","key":"{inner.key}"}}
	]}`))
	if err != nil {
		t.Fatalf("parse rules: %v", err)
	}
	for _, p := range problems {
		t.Fatalf("rule problem: %v", p)
	}
	set.Rules = f.Rules

	idx := New()
	idx.SetRules(set)
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}

	// Collect what each side found, as key+site pairs.
	collect := func(kind string) []string {
		var out []string
		for _, b := range sv.Outbound {
			if b.Kind != kind {
				continue
			}
			out = append(out, b.Key+" @ "+string(b.Site))
		}
		sort.Strings(out)
		return out
	}
	builtin := collect("grpc.method")
	configured := collect("rule.grpc")

	if len(builtin) == 0 {
		t.Fatal("the built-in pass found nothing; the fixture or the pass changed")
	}
	if len(configured) == 0 {
		t.Fatalf("the configured rule found nothing — the vocabulary cannot state the built-in shape.\nbuilt-in found:\n  %v", builtin)
	}

	// The built-in normalizes keys (it strips the leading slash of a method
	// path); the rule emits what the constant literally says. Compare on the
	// normalized form so the test is about *which edges were found*, not about
	// string cosmetics.
	norm := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[trimLeadingSlash(x)] = true
		}
		return m
	}
	want, got := norm(builtin), norm(configured)

	// Inventing an edge is the unforgivable failure: it puts a call in the
	// surface that the service does not make. Nothing may be invented, ever.
	for k := range got {
		if !want[k] {
			t.Errorf("configured rules invented an edge the built-in did not find: %s", k)
		}
	}

	// The known gap, pinned rather than hidden.
	//
	// Phase 1 asks what a function's own body does, and it can only ask that
	// of functions unfold observed — which is owned code, because admitting
	// dependency call sites would bury the real edges under library plumbing.
	// So a hand-written SDK *in a dependency*, where the transport call is
	// several hops inside someone else's module, is invisible to a rule while
	// the built-in still finds it: the built-in works over the whole index and
	// walks back to the owned caller, rather than matching at a call site.
	//
	// Raising the phase-1 depth does not fix this and makes it worse — at
	// depth 2 the marks propagate up through this project's own helpers and
	// main() acquires edges it does not make, which is the exact failure this
	// fixture was built to pin. The fix is to let phase 1 see dependency
	// bodies while attribution still stops at the owned caller, which is
	// engine work rather than vocabulary work.
	knownGap := map[string]bool{
		"accountgroup.v1.AccountGroupService/GetMulti @ (*example.com/conversation.Server).listAccounts": true,
		"accountgroup.v1.AccountGroupService/GetMulti @ (*example.com/conversation.Server).syncAll":      true,
		"accountgroup.v1.AccountGroupService/Create @ (*example.com/conversation.Server).syncAll":        true,
	}
	for k := range want {
		if got[k] {
			if knownGap[k] {
				t.Errorf("known gap %q is now covered — delete it from knownGap", k)
			}
			continue
		}
		if !knownGap[k] {
			t.Errorf("configured rules missed an edge the built-in found: %s", k)
		}
	}

	// And the engine's own guarantees still apply to rule output: a configured
	// binding is never "exact", and it names the rule that produced it.
	for _, b := range sv.Outbound {
		if b.Kind != "rule.grpc" {
			continue
		}
		if b.Confidence == model.ConfExact {
			t.Errorf("a configured shape must not claim exact: %+v", b)
		}
		if b.Detail == "" {
			t.Errorf("a configured binding must name its rule: %+v", b)
		}
	}
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}
