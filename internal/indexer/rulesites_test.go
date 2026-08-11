package indexer

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/rules"
)

// A call site knows which recognizers claimed it, built-ins included.
//
// Built-ins are the point. They are the majority of matches in most repos, and
// they don't go through the rule evaluator at all — so an answer assembled only
// from configured rules would report "nothing recognizes this" on the call that
// produced the service's entire inbound surface.
func TestRulesAtNamesTheBuiltinThatMatched(t *testing.T) {
	dir, err := filepath.Abs("testdata/routes")
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
	var route *struct {
		file string
		line int
		rule string
	}
	for _, b := range sv.Inbound {
		if b.Kind == "http.route" && strings.Contains(b.Key, "/orders") {
			route = &struct {
				file string
				line int
				rule string
			}{b.File, b.Line, b.Rule}
		}
	}
	if route == nil {
		t.Fatal("expected the orders route among the inbound bindings")
	}
	if route.rule != "builtin.http.routes" {
		t.Errorf("a binding should name the rule that produced it, got %q", route.rule)
	}
	got := idx.RulesAt(route.file, route.line)
	if !slices.Contains(got, "builtin.http.routes") {
		t.Errorf("RulesAt(%s:%d) = %v, want it to name builtin.http.routes", route.file, route.line, got)
	}

	// And a line nothing recognizes says so, rather than inheriting a
	// neighbour's answer.
	if r := idx.RulesAt(route.file, route.line+100000); len(r) != 0 {
		t.Errorf("RulesAt on an unmatched line = %v, want none", r)
	}
}

// The report exists so a rule that quietly stopped matching says so. Built-ins
// were exempt from that — they reported no count at all — which left the three
// rules most likely to break on a library upgrade as the ones you couldn't
// check.
func TestRuleReportCountsBuiltinMatches(t *testing.T) {
	dir, err := filepath.Abs("testdata/routes")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, r := range idx.RuleReport().Rules {
		if r.ID == "builtin.http.routes" {
			if r.Matches == 0 {
				t.Error("builtin.http.routes matched two routes in this fixture but reported 0")
			}
			return
		}
	}
	t.Fatal("builtin.http.routes missing from the report")
}

// A configured rule's body comes back with it, so it can be read and edited
// where it is seen rather than by going to find the file.
func TestRuleReportCarriesTheRuleBody(t *testing.T) {
	dir, err := filepath.Abs("testdata/routes")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	set := rules.Load()
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"test.http.get",
	   "match":{"package":"net/http","func":"Get","minArgs":1},
	   "emit":{"role":"outbound","kind":"rule.http","key":"{arg0}"}}
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
	for _, r := range idx.RuleReport().Rules {
		if r.ID != "test.http.get" {
			continue
		}
		if len(r.Spec) == 0 {
			t.Fatal("a configured rule should carry its own body")
		}
		if !strings.Contains(string(r.Spec), `"func":"Get"`) {
			t.Errorf("spec should be the rule as written, got %s", r.Spec)
		}
		if r.Matches == 0 {
			t.Error("the rule matches http.Get in this fixture but reported 0")
		}
		return
	}
	t.Fatal("test.http.get missing from the report")
}
