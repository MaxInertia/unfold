package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/platform"
)

func call(site, pkg, recv, fn string, args ...string) platform.Call {
	c := platform.Call{
		PkgPath: pkg, Recv: recv, RecvPkg: pkg, Func: fn,
		Site: model.TargetID(site), File: "/x/" + site + ".go", Line: 1,
	}
	for _, a := range args {
		c.Args = append(c.Args, platform.Arg{Value: a, Known: a != ""})
	}
	return c
}

func mustParse(t *testing.T, body string) []Rule {
	t.Helper()
	f, problems, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, p := range problems {
		t.Fatalf("unexpected rule problem: %v", p)
	}
	return f.Rules
}

// The motivating case, and the one that decides whether the vocabulary is
// worth anything: an SDK method is interesting because of what it does
// *inside*, not because of its name. Nothing at the call site says "gRPC" —
// only the callee's body does.
func TestCalleeMatchesRecognizesAWrapper(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"grpc.invoke",
	   "match":{"package":"google.golang.org/grpc","recv":"ClientConn","func":"Invoke","minArgs":2},
	   "emit":{"role":"outbound","kind":"grpc.method","key":"{arg1}"}},
	  {"id":"sdk.client",
	   "match":{"package":"foo/$(domain)/sdks/go","calleeMatches":{"rule":"grpc.invoke","depth":1}},
	   "emit":{"role":"outbound","kind":"grpc.method","key":"{inner.key}"}}
	]}`)

	e := NewEvaluator(rs)
	// The SDK method's body issues the transport call.
	e.Observe(call("sdkGet", "google.golang.org/grpc", "ClientConn", "Invoke", "", "/orders.v1.Orders/Get"), "")
	// Our code calls the SDK method, and says nothing about gRPC.
	e.Observe(call("handler", "foo/orders/sdks/go", "Client", "Get"), "sdkGet")

	got := e.Run()
	var outbound []model.Binding
	for _, b := range got {
		if b.Kind == "grpc.method" && b.Site == "handler" {
			outbound = append(outbound, b)
		}
	}
	if len(outbound) != 1 {
		t.Fatalf("expected one edge attributed to our code, got %+v", got)
	}
	if outbound[0].Key != "/orders.v1.Orders/Get" {
		t.Errorf("the key comes from inside the SDK, got %q", outbound[0].Key)
	}
	if outbound[0].Confidence != model.ConfDeclared {
		t.Errorf("a configured shape is declared, got %q", outbound[0].Confidence)
	}
	if !strings.Contains(outbound[0].Detail, "sdk.client") {
		t.Errorf("provenance should name the rule, got %q", outbound[0].Detail)
	}
}

// Depth is the bound that unbounded chain-following taught the gRPC pass to
// need. At depth 1 a wrapper-of-a-wrapper must NOT match, or every caller
// upstream inherits an edge it doesn't make.
func TestCalleeDepthIsBounded(t *testing.T) {
	rules := func(depth string) []Rule {
		return mustParse(t, `{"rules":[
		  {"id":"inner","match":{"package":"transport","func":"Send"},
		   "emit":{"role":"outbound","kind":"k","key":"{arg0}"}},
		  {"id":"outer","match":{"package":"app","calleeMatches":{"rule":"inner","depth":`+depth+`}},
		   "emit":{"role":"outbound","kind":"k","key":"{inner.key}"}}
		]}`)
	}
	build := func(depth string) []model.Binding {
		e := NewEvaluator(rules(depth))
		e.Observe(call("lvl1", "transport", "", "Send", "topic-a"), "")
		e.Observe(call("lvl2", "app", "", "Wrap"), "lvl1")   // one hop away
		e.Observe(call("lvl3", "app", "", "Wrap2"), "lvl2")  // two hops away
		return e.Run()
	}

	at1 := build("1")
	sites := map[model.TargetID]bool{}
	for _, b := range at1 {
		sites[b.Site] = true
	}
	if !sites["lvl2"] {
		t.Error("depth 1 must match the direct wrapper")
	}
	if sites["lvl3"] {
		t.Error("depth 1 must NOT reach through a second wrapper")
	}

	sites = map[model.TargetID]bool{}
	for _, b := range build("2") {
		sites[b.Site] = true
	}
	if !sites["lvl3"] {
		t.Error("depth 2 was asked for and should reach the second wrapper")
	}
}

// A key that can't be resolved is skipped rather than guessed at — the same
// discipline as an outbound call whose URL is built at runtime.
func TestUnresolvableKeyIsSkipped(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"pub","match":{"package":"acme/events","func":"Publish"},
	   "emit":{"role":"outbound","kind":"pubsub.topic","key":"{arg0}"}}
	]}`)
	e := NewEvaluator(rs)
	e.Observe(call("a", "acme/events", "", "Publish", "orders-v1"), "")
	e.Observe(call("b", "acme/events", "", "Publish", ""), "") // runtime value
	got := e.Run()
	if len(got) != 1 || got[0].Site != "a" {
		t.Errorf("only the resolvable key should produce a binding, got %+v", got)
	}
	if e.Stats["pub"] != 1 {
		t.Errorf("stats should count matches that produced a binding, got %d", e.Stats["pub"])
	}
}

func TestPackagePatternsAndCaptures(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
		capture       string
	}{
		{"foo/$(domain)/sdks/go", "foo/orders/sdks/go", true, "orders"},
		{"foo/$(domain)/sdks/go", "foo/orders/extra/sdks/go", false, ""},
		{"foo/**/sdks/go", "foo/a/b/sdks/go", true, ""},
		{"foo/*/sdks", "foo/orders/sdks", true, ""},
		{"foo/*/sdks", "foo/sdks", false, ""},
		{"net/http", "net/http", true, ""},
		{"net/http", "net/http/httptest", false, ""},
		// A capture must not match an empty segment, or "foo//sdks" would
		// silently produce an empty domain.
		{"foo/$(domain)/sdks", "foo//sdks", false, ""},
	}
	for _, c := range cases {
		caps := Captures{}
		if got := matchPattern(c.pattern, c.path, caps); got != c.want {
			t.Errorf("%q vs %q: got %v want %v", c.pattern, c.path, got, c.want)
		}
		if c.capture != "" && caps["domain"] != c.capture {
			t.Errorf("%q vs %q: capture got %q want %q", c.pattern, c.path, caps["domain"], c.capture)
		}
	}
}

// Validation refuses the rules whose failure mode is silence: one that matches
// everything, one that can never fire, one claiming a confidence it hasn't
// earned.
func TestValidationRefusesSilentFailures(t *testing.T) {
	bad := []string{
		`{"rules":[{"id":"x","match":{},"emit":{"role":"outbound","kind":"k","key":"a"}}]}`,
		`{"rules":[{"id":"x","match":{"func":"F"},"emit":{"role":"sideways","kind":"k","key":"a"}}]}`,
		`{"rules":[{"id":"x","match":{"func":"F"},"emit":{"role":"outbound","kind":"k","key":"a","confidence":"exact"}}]}`,
		`{"rules":[{"id":"x","match":{"func":"F"},"emit":{"role":"outbound","kind":"","key":"a"}}]}`,
		`{"rules":[{"id":"x","match":{"func":"F","calleeMatches":{"rule":"x"}},"emit":{"role":"outbound","kind":"k","key":"a"}}]}`,
		`{"rules":[{"id":"a","match":{"func":"F"},"emit":{"role":"outbound","kind":"k","key":"a"}},
		           {"id":"a","match":{"func":"G"},"emit":{"role":"outbound","kind":"k","key":"b"}}]}`,
	}
	for _, body := range bad {
		f, problems, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(problems) == 0 {
			t.Errorf("expected a problem for %s (kept %d rules)", body, len(f.Rules))
		}
	}
}

// A calleeMatches pointing at a disabled rule can never fire. Silence is the
// failure mode, so it's reported rather than left to look like "nothing found".
func TestDanglingAndDisabledReferencesAreReported(t *testing.T) {
	off := false
	rs := []Rule{
		{ID: "inner", Enabled: &off, Match: &Match{Func: "Send"}, Emit: &Emit{Role: "outbound", Kind: "k", Key: "x"}},
		{ID: "outer", Match: &Match{Func: "Wrap", CalleeMatches: &Nested{Rule: "inner"}}, Emit: &Emit{Role: "outbound", Kind: "k", Key: "{inner.key}"}},
		{ID: "ghost", Match: &Match{Func: "W", CalleeMatches: &Nested{Rule: "nope"}}, Emit: &Emit{Role: "outbound", Kind: "k", Key: "{inner.key}"}},
	}
	problems := Validate(rs)
	if len(problems) != 2 {
		t.Fatalf("expected two problems, got %v", problems)
	}
	joined := problems[0].Error() + " " + problems[1].Error()
	if !strings.Contains(joined, "disabled") || !strings.Contains(joined, "unknown") {
		t.Errorf("problems should name both causes, got %v", problems)
	}
}

// Disabling is how a built-in is switched off, and "absent" must not read as
// "re-enabled" — which is why Enabled is a pointer.
func TestDisabledRulesDoNotRun(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"pub","enabled":false,"match":{"package":"acme/events","func":"Publish"},
	   "emit":{"role":"outbound","kind":"pubsub.topic","key":"{arg0}"}}
	]}`)
	e := NewEvaluator(rs)
	e.Observe(call("a", "acme/events", "", "Publish", "orders-v1"), "")
	if got := e.Run(); len(got) != 0 {
		t.Errorf("a disabled rule must produce nothing, got %+v", got)
	}
}

// The emitted surface must not depend on map iteration order — the same
// stability that binding order needed after it made a test a coin flip.
func TestOutputIsDeterministic(t *testing.T) {
	rs := mustParse(t, `{"rules":[
	  {"id":"b","match":{"package":"p","func":"F"},"emit":{"role":"outbound","kind":"k","key":"{arg0}"}},
	  {"id":"a","match":{"package":"p","func":"F"},"emit":{"role":"inbound","kind":"k","key":"{arg0}"}}
	]}`)
	var first string
	for i := 0; i < 20; i++ {
		e := NewEvaluator(rs)
		for _, s := range []string{"s3", "s1", "s2"} {
			e.Observe(call(s, "p", "", "F", "key-"+s), "")
		}
		var b strings.Builder
		for _, x := range e.Run() {
			b.WriteString(string(x.Role) + ":" + x.Key + ":" + string(x.Site) + ";")
		}
		if i == 0 {
			first = b.String()
		} else if b.String() != first {
			t.Fatalf("run %d differed:\n%s\n%s", i, first, b.String())
		}
	}
}

// A `serves` entry is a declaration rather than a recognizer: it says a
// service is the inbound end of some keys, whatever the code does or doesn't
// show.
func TestServesEntriesParse(t *testing.T) {
	f, problems, err := Parse([]byte(`{
	  "serves": [
	    {"//": "the whole gRPC service", "kind": "grpc.method", "keys": ["notes.v1.NotesService/*"]},
	    {"service": "billing", "kind": "http.route", "keys": ["GET /v1/invoices", "POST /v1/invoices"]}
	  ]
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
	if len(f.Serves) != 2 {
		t.Fatalf("expected 2 entries, got %+v", f.Serves)
	}
	if f.Serves[1].Service != "billing" || len(f.Serves[1].Keys) != 2 {
		t.Errorf("an entry may name the service it is about: %+v", f.Serves[1])
	}
}

// The refusals, each for a claim far larger than its author meant.
func TestServesEntriesAreValidated(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"no kind", `{"serves": [{"keys": ["a"]}]}`},
		{"no keys", `{"serves": [{"kind": "grpc.method"}]}`},
		{"empty key", `{"serves": [{"kind": "grpc.method", "keys": [" "]}]}`},
		{"every key of its kind", `{"serves": [{"kind": "grpc.method", "keys": ["/*"]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, problems, err := Parse([]byte(tc.json))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(problems) == 0 {
				t.Errorf("expected a problem, got none")
			}
			if len(f.Serves) != 0 {
				t.Errorf("an invalid entry must be dropped, got %+v", f.Serves)
			}
		})
	}
}

func TestKeyMatches(t *testing.T) {
	cases := []struct {
		declared, key string
		want          bool
	}{
		{"a.v1.S/Get", "a.v1.S/Get", true},
		{"a.v1.S/Get", "a.v1.S/Put", false},
		{"a.v1.S/*", "a.v1.S/Get", true},
		{"a.v1.S/*", "a.v1.Other/Get", false},
		// A prefix that stops mid-name is not a match: "a.v1.S" must not
		// answer for "a.v1.Sidecar".
		{"a.v1.S/*", "a.v1.S", false},
	}
	for _, tc := range cases {
		if got := KeyMatches(tc.declared, tc.key); got != tc.want {
			t.Errorf("KeyMatches(%q, %q) = %v, want %v", tc.declared, tc.key, got, tc.want)
		}
	}
}

// An unnamed entry means "the repository this file is in", so a shared file
// carrying one would say every repository in the workspace serves it.
func TestAnUnnamedServesEntryNeedsARepoLocalFile(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "org.json")
	writeFile(t, shared, `{"serves": [{"kind": "grpc.method", "keys": ["a.v1.S/Get"]}]}`)

	repo := t.TempDir()
	writeFile(t, RepoPath(repo), `{"serves": [{"kind": "grpc.method", "keys": ["b.v1.S/Get"]}]}`)

	set := Load(shared, RepoPath(repo))
	if len(set.Problems) != 1 {
		t.Fatalf("the shared file's unnamed entry should be reported, got %v", set.Problems)
	}
	if len(set.Serves) != 1 || set.Serves[0].Keys[0] != "b.v1.S/Get" {
		t.Fatalf("the repo-local entry should survive, got %+v", set.Serves)
	}
	// And it applies to the repository it was found in, by no name at all.
	if got := set.ServesFor(repo); len(got) != 1 {
		t.Errorf("an unnamed entry belongs to its own repo, got %+v", got)
	}
	if got := set.ServesFor(t.TempDir(), "other"); len(got) != 0 {
		t.Errorf("and to no other, got %+v", got)
	}
}

// A named entry applies wherever the service goes by that name — the directory
// or whatever the manifest calls it.
func TestServesForMatchesEitherName(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "org.json")
	writeFile(t, shared, `{"serves": [{"service": "conversation", "kind": "grpc.method", "keys": ["c.v1.S/Get"]}]}`)
	set := Load(shared)

	if got := set.ServesFor(t.TempDir(), "conversation"); len(got) != 1 {
		t.Errorf("expected the entry to apply to conversation, got %+v", got)
	}
	if got := set.ServesFor(t.TempDir(), "inbox"); len(got) != 0 {
		t.Errorf("expected nothing for another service, got %+v", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
