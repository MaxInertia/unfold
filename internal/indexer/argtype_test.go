package indexer

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/MaxInertia/unfold/internal/rules"
)

// "Emit" names half the event-publishing methods ever written. A rule that
// matches on the method name claims all of them, and the usual narrowing
// doesn't help: the call goes through an *interface*, so the receiver's
// package is wherever that interface was declared — and nothing stops a repo
// from declaring its own with the same method, which the local fixture does.
//
// What doesn't move is the type crossing the call. This pins that a rule can
// say so, and that saying so is what separates the two calls.
func TestArgTypeSeparatesTwoEmitMethods(t *testing.T) {
	dir, err := filepath.Abs("testdata/emitters")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	keys := func(ruleJSON string) []string {
		t.Helper()
		set := rules.Load()
		f, problems, err := rules.Parse([]byte(ruleJSON))
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
		var out []string
		for _, b := range sv.Outbound {
			if b.Kind == "test.topic" {
				out = append(out, b.Key)
			}
		}
		sort.Strings(out)
		return out
	}

	// The rule as first written: the name, and nothing else.
	byName := keys(`{"rules":[
	  {"id":"test.emit.name",
	   "match":{"func":"Emit"},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`)
	if len(byName) != 2 {
		t.Fatalf("matching on the name alone should claim both Emit calls, got %v", byName)
	}

	// The same rule, constrained by the type that identifies the library.
	byType := keys(`{"rules":[
	  {"id":"test.emit.typed",
	   "match":{"func":"Emit","args":[{"index":2,"type":"*example.com/emitters/sdk.Event"}]},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`)
	if len(byType) != 1 || byType[0] != "orders-v1" {
		t.Errorf("constraining argument 2 to the SDK event type should claim only the SDK call, got %v", byType)
	}

	// The declared parameter type answers the same question from the other
	// side, which is what a caller passing a local implementation needs.
	byParam := keys(`{"rules":[
	  {"id":"test.emit.param",
	   "match":{"func":"Emit","args":[{"index":2,"paramType":"*example.com/emitters/sdk.Event"}]},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`)
	if len(byParam) != 1 || byParam[0] != "orders-v1" {
		t.Errorf("constraining on the declared parameter type should claim only the SDK call, got %v", byParam)
	}

	// A wildcard over the module, which is how you'd write it for a family of
	// generated payload types.
	byGlob := keys(`{"rules":[
	  {"id":"test.emit.glob",
	   "match":{"func":"Emit","args":[{"index":2,"type":"*example.com/emitters/sdk.*"}]},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`)
	if len(byGlob) != 1 || byGlob[0] != "orders-v1" {
		t.Errorf("a wildcard type pattern should claim only the SDK call, got %v", byGlob)
	}

	// The pointer is part of the pattern. A rule written against the value
	// type doesn't fire — visible and fixable, where quietly accepting both
	// would put edges in the surface nobody asked for.
	byValue := keys(`{"rules":[
	  {"id":"test.emit.value",
	   "match":{"func":"Emit","args":[{"index":2,"type":"example.com/emitters/sdk.Event"}]},
	   "emit":{"role":"outbound","kind":"test.topic","key":"{arg1}"}}
	]}`)
	if len(byValue) != 0 {
		t.Errorf("the value type is not the pointer type; want no matches, got %v", byValue)
	}
}

// An argument constraint on a position the call doesn't have is a rejection,
// not something to skip: "argument 2 is an Event" must not hold for a call
// with two arguments.
func TestArgTypeRejectsShortCalls(t *testing.T) {
	dir, err := filepath.Abs("testdata/routes")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	set := rules.Load()
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"test.get.arg9",
	   "match":{"package":"net/http","func":"Get","args":[{"index":9,"type":"string"}]},
	   "emit":{"role":"outbound","kind":"test.short","key":"{arg0}"}}
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
	for _, b := range sv.Outbound {
		if b.Kind == "test.short" {
			t.Errorf("a constraint on argument 9 must not match a one-argument call: %+v", b)
		}
	}
}
