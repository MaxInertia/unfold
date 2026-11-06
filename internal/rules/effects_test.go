package rules

import (
	"strings"
	"testing"
)

// An effect needs a kind and nothing else; the fields that only mean something
// on an edge are refused, so a rule written as an edge by mistake is caught
// rather than silently producing a badge with a hole in it.
func TestEffectRuleValidation(t *testing.T) {
	ok := mustParse(t, `{"rules":[
	  {"id":"e.nokey", "match":{"package":"os","func":"Getenv"},
	   "emit":{"role":"effect","kind":"env","access":"read"}}
	]}`)
	if len(ok) != 1 || !ok[0].IsEffect() {
		t.Fatalf("an effect rule without a key must parse, got %+v", ok)
	}

	bad := map[string]string{
		"no kind":    `{"id":"x","match":{"func":"F"},"emit":{"role":"effect"}}`,
		"bad access": `{"id":"x","match":{"func":"F"},"emit":{"role":"effect","kind":"db","access":"maybe"}}`,
		"handler":    `{"id":"x","match":{"func":"F"},"emit":{"role":"effect","kind":"db","handler":"arg0"}}`,
		"confidence": `{"id":"x","match":{"func":"F"},"emit":{"role":"effect","kind":"db","confidence":"inferred"}}`,
		"leaf":       `{"id":"x","match":{"func":"F"},"emit":{"role":"effect","kind":"db"},"leaf":{"expand":false}}`,
		"edge access": `{"id":"x","match":{"func":"F"},
		                 "emit":{"role":"outbound","kind":"k","key":"{arg0}","access":"read"}}`,
	}
	for name, body := range bad {
		_, problems, err := Parse([]byte(`{"rules":[` + body + `]}`))
		if err != nil {
			t.Fatalf("%s: parse error %v", name, err)
		}
		if len(problems) != 1 {
			t.Errorf("%s: want one problem, got %v", name, problems)
		}
	}
}

// An effect is recorded against the function that holds it with the callee
// named and the key resolved when it can be — and it is not a binding.
func TestEffectRuleRecordsEvidenceNotEdges(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"env.read", "match":{"package":"os","func":"Getenv"},
	   "emit":{"role":"effect","kind":"env","access":"read","key":"{arg0}"}}
	]}`)
	e := NewEvaluator(rs)
	e.Observe(call("loadConfig", "os", "", "Getenv", "PORT"))
	e.Observe(call("loadDynamic", "os", "", "Getenv", "")) // name not static

	if got := e.Run(); len(got) != 0 {
		t.Errorf("an effect rule must not produce bindings, got %+v", got)
	}
	effs := e.Effects()
	if len(effs["loadConfig"]) != 1 {
		t.Fatalf("loadConfig: want one effect, got %+v", effs["loadConfig"])
	}
	got := effs["loadConfig"][0]
	if got.Kind != "env" || got.Access != "read" || got.Key != "PORT" || got.Callee != "os.Getenv" || got.Rule != "env.read" {
		t.Errorf("evidence = %+v", got)
	}
	// Unlike a binding, an unresolvable key doesn't drop the effect: the read
	// happens whether or not the name is static.
	if len(effs["loadDynamic"]) != 1 || effs["loadDynamic"][0].Key != "" {
		t.Errorf("loadDynamic: want the effect with an empty key, got %+v", effs["loadDynamic"])
	}
	if e.Stats["env.read"] != 2 {
		t.Errorf("stats = %d, want 2", e.Stats["env.read"])
	}
	if m := e.Matched()["/x/loadConfig.go:1"]; len(m) != 1 || m[0] != "env.read" {
		t.Errorf("the site should know the rule that claimed it, got %v", m)
	}
}

// funcOnly is what lets a rule mean http.Get and not http.Header.Get: same
// package, same name, and the argument types don't tell them apart.
func TestFuncOnlyExcludesMethods(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"http.get", "match":{"package":"net/http","func":"Get","funcOnly":true},
	   "emit":{"role":"effect","kind":"net"}}
	]}`)
	e := NewEvaluator(rs)
	e.Observe(call("fetch", "net/http", "", "Get", "http://x"))
	e.Observe(call("inspect", "net/http", "Header", "Get", "Content-Type"))
	e.Run()
	if len(e.Effects()["fetch"]) != 1 {
		t.Errorf("http.Get should match, got %+v", e.Effects()["fetch"])
	}
	if len(e.Effects()["inspect"]) != 0 {
		t.Errorf("Header.Get must not match a funcOnly rule, got %+v", e.Effects()["inspect"])
	}
}

// The shipped vocabulary parses, is addressable by id, and sits under every
// configured tier so a repo file can switch one off.
func TestBuiltinEffectsShipWithLoad(t *testing.T) {
	bs := BuiltinEffects()
	if len(bs) == 0 {
		t.Fatal("no built-in effect rules")
	}
	for _, r := range bs {
		if !strings.HasPrefix(r.ID, "effect.") || !r.IsEffect() {
			t.Errorf("built-in %q should be an effect rule under the effect. prefix", r.ID)
		}
	}
	set := Load()
	if len(set.Problems) != 0 {
		t.Errorf("problems: %v", set.Problems)
	}
	if set.Sources["effect.os.getenv"] != BuiltinSource {
		t.Errorf("built-ins should be sourced as %q, got %q", BuiltinSource, set.Sources["effect.os.getenv"])
	}
	// CalleeName is what the evidence shows.
	if got := CalleeName(call("f", "database/sql", "DB", "Exec")); got != "sql.DB.Exec" {
		t.Errorf("CalleeName = %q", got)
	}
}
