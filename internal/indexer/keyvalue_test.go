package indexer

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

// Keys are rarely literals at the call site. A shared events package names its
// topics as constants, as package-level vars, or as fields of one exported
// registry struct — and unfold has to reach all of them, because a key it
// can't resolve is an edge that silently isn't there.
//
// Constants already worked: the type checker folds them, across packages and
// through concatenation. The two variable shapes did not, and this pins both,
// along with the confidence each is entitled to claim.
func TestKeysFromConstantsAndVariables(t *testing.T) {
	dir, err := filepath.Abs("testdata/keys")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	set := rules.Load()
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"test.emit",
	   "match":{"func":"Emit","minArgs":2},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("rule problem: %v", problems[0])
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

	conf := map[string]model.BindingConfidence{}
	var got []string
	for _, b := range sv.Outbound {
		if b.Kind != "test.topic" {
			continue
		}
		got = append(got, b.Key)
		conf[b.Key] = b.Confidence
	}
	sort.Strings(got)

	want := []string{
		"acme.orders.cancelled", // field of an anonymous struct var
		"acme.orders.created",   // literal + constant, folded
		"acme.orders.refund",    // field of a struct var, read by another call
		"acme.orders.shipped",   // field of a struct var
		"acme.payments.taken",   // package-level var
		"acme.users.created",    // constant built from constants
		"literal.key",
		"orders.created", // exported constant from another package
	}
	if len(got) != len(want) {
		t.Fatalf("resolved %d keys, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for n := range want {
		if got[n] != want[n] {
			t.Errorf("key %d: got %q, want %q", n, got[n], want[n])
		}
	}

	// What each key is entitled to claim. A constant is the value itself; a
	// variable's initializer is only what the program started with, and an
	// assignment made elsewhere is invisible from here — so the badge has to
	// say so rather than presenting the two as equally certain.
	exact := []string{"literal.key", "orders.created", "acme.users.created", "acme.orders.created"}
	for _, k := range exact {
		if c := conf[k]; c != model.ConfDeclared && c != model.ConfExact {
			t.Errorf("%q comes from a constant; got confidence %q", k, c)
		}
	}
	inferred := []string{"acme.payments.taken", "acme.orders.shipped", "acme.orders.cancelled"}
	for _, k := range inferred {
		if conf[k] != model.ConfInferred {
			t.Errorf("%q comes from a variable's initializer, so it must be inferred; got %q", k, conf[k])
		}
	}
}

// The same resolution reaches the built-in recognizers, and takes the same
// honesty with it: a route registered with a package-level pattern var is
// found, and is not claimed as exact.
func TestBuiltinRouteFromVariablePattern(t *testing.T) {
	dir, err := filepath.Abs("testdata/keys")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	var found *model.Binding
	for n := range sv.Inbound {
		if sv.Inbound[n].Key == "/from-var" {
			found = &sv.Inbound[n]
		}
	}
	if found == nil {
		t.Fatalf("a route registered with a var pattern should be found; inbound: %+v", sv.Inbound)
	}
	if found.Confidence != model.ConfInferred {
		t.Errorf("a key read from a var initializer is not exact; got %q", found.Confidence)
	}
}
