package indexer

import (
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

// An event identified by a *field* of a struct the call site passes whole.
//
// The shapes already resolved all put the string where the argument is: a
// literal, a constant, a var holding a string, or a selector naming the field
// (`topics.Topics.Shipped`). Here the argument is the definition itself —
// `Emit(ctx, events.FooEventDefn, nil)` — and the key is inside it, so nothing
// in the expression says which part of the value identifies the event. That is
// a question only the rule can answer, so the key template has to be able to
// name the field.
func TestKeyFromFieldOfStructArgument(t *testing.T) {
	dir, err := filepath.Abs("testdata/eventdefn")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	set := rules.Load()
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"sdk.emit",
	   "match":{"func":"Emit","minArgs":2},
	   "emit":{"role":"outbound","kind":"sdk.event","key":"{arg1.ID}"}},
	  {"id":"sdk.subscribe",
	   "match":{"func":"Subscribe","minArgs":1},
	   "emit":{"role":"inbound","kind":"sdk.event","key":"{arg0.ID}","target":"{arg1}"}}
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

	out := map[string]model.Binding{}
	for _, b := range sv.Outbound {
		if b.Kind == "sdk.event" {
			out[b.Key] = b
		}
	}
	in := map[string]model.Binding{}
	for _, b := range sv.Inbound {
		if b.Kind == "sdk.event" {
			in[b.Key] = b
		}
	}

	for _, key := range []string{"foo-happened", "bar-happened"} {
		if _, ok := out[key]; !ok {
			t.Errorf("no outbound binding keyed %q; got %v", key, keysOf(out))
		}
	}
	if _, ok := in["foo-happened"]; !ok {
		t.Errorf("no inbound binding keyed %q; got %v", "foo-happened", keysOf(in))
	}

	// The join is the point: both ends have to land on the same key, or the
	// publisher and the subscriber are two unrelated edges.
	if len(out) > 0 && len(in) > 0 {
		if _, ok := out["foo-happened"]; ok {
			if _, ok := in["foo-happened"]; !ok {
				t.Error("emit and subscribe resolved different keys for the same definition")
			}
		}
	}

	// A field read out of a var's initializer is what the program starts with,
	// not what it is — the same claim `topics.Topics.Shipped` makes.
	if b, ok := out["foo-happened"]; ok && b.Confidence != model.ConfInferred {
		t.Errorf("key from a var's field must be inferred; got %q", b.Confidence)
	}
}

func keysOf(m map[string]model.Binding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
