package difftour

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/model"
)

// graphEngine is a model.Engine over a fixture call graph: each function's
// callees in call order, from which Usages is the reverse index, exactly as
// the indexer derives it.
type graphEngine struct {
	calls map[model.TargetID][]model.TargetID
	eps   []model.Entrypoint
	// refs are value references (callbacks), which neither direction follows.
	refs map[model.TargetID][]model.TargetID
}

func (g graphEngine) Frame(id model.TargetID) (*model.Frame, error) {
	callees, ok := g.calls[id]
	if !ok {
		return nil, errors.New("unknown")
	}
	f := &model.Frame{ID: id}
	for _, c := range callees {
		f.Calls = append(f.Calls, model.CallSite{TargetID: c, Kind: model.KindDirect})
	}
	for _, c := range g.refs[id] {
		f.Calls = append(f.Calls, model.CallSite{TargetID: c, Kind: model.KindRef})
	}
	return f, nil
}

func (g graphEngine) Usages(id model.TargetID) ([]model.Usage, error) {
	var out []model.Usage
	for caller, callees := range g.calls {
		for _, c := range callees {
			if c == id {
				out = append(out, model.Usage{Caller: caller, Kind: model.UsageCall})
			}
		}
	}
	for caller, callees := range g.refs {
		for _, c := range callees {
			if c == id {
				out = append(out, model.Usage{Caller: caller, Kind: model.UsageRef})
			}
		}
	}
	return out, nil
}

func (g graphEngine) Entrypoints() []model.Entrypoint                       { return g.eps }
func (g graphEngine) LookupSymbol(string) (model.TargetID, error)           { return "", nil }
func (g graphEngine) FrameForCall(model.CallID, int) (*model.Frame, error)  { return nil, nil }
func (g graphEngine) Search(string, int) []model.SearchResult               { return nil }
func (g graphEngine) Files() []string                                       { return nil }
func (g graphEngine) TypeInfo(model.TargetID, int) (*model.TypeInfo, error) { return nil, nil }

// noEntrypoints is the same graph without knowledge of where execution starts:
// a field rather than an embedding, so Entrypoints is not in its method set.
type noEntrypoints struct{ g graphEngine }

func (n noEntrypoints) Frame(id model.TargetID) (*model.Frame, error)       { return n.g.Frame(id) }
func (n noEntrypoints) Usages(id model.TargetID) ([]model.Usage, error)     { return n.g.Usages(id) }
func (noEntrypoints) LookupSymbol(string) (model.TargetID, error)           { return "", nil }
func (noEntrypoints) FrameForCall(model.CallID, int) (*model.Frame, error)  { return nil, nil }
func (noEntrypoints) Search(string, int) []model.SearchResult               { return nil }
func (noEntrypoints) Files() []string                                       { return nil }
func (noEntrypoints) TypeInfo(model.TargetID, int) (*model.TypeInfo, error) { return nil, nil }

const pkg = "example.com/m/pkg."

func id(s string) model.TargetID { return model.TargetID(pkg + s) }

// The fixture:
//
//	Main (main) → A → B → D
//	Main        → C
//	Route (http.route) → B
//	Main        → X   (as a value reference only)
//	X calls nothing and is reached by nothing that counts.
func fixture() graphEngine {
	return graphEngine{
		calls: map[model.TargetID][]model.TargetID{
			id("Main"):  {id("A"), id("C")},
			id("A"):     {id("B")},
			id("B"):     {id("D")},
			id("C"):     {},
			id("D"):     {},
			id("Route"): {id("B")},
			id("X"):     {},
		},
		refs: map[model.TargetID][]model.TargetID{id("Main"): {id("X")}},
		eps: []model.Entrypoint{
			{ID: id("Main"), Title: "main", Kind: "main"},
			{ID: id("Route"), Title: "Route", Kind: "http.route", Key: "GET /r"},
		},
	}
}

func changes(ids ...string) []diff.Change {
	out := make([]diff.Change, 0, len(ids))
	for n, s := range ids {
		out = append(out, diff.Change{ID: id(s), Title: s, File: "a.go", Line: n + 1, Status: "modified", Added: 1})
	}
	return out
}

func symbols(fns []Function) []string {
	out := make([]string, 0, len(fns))
	for _, f := range fns {
		out = append(out, f.Symbol)
	}
	return out
}

func TestComputeOrdersByExecution(t *testing.T) {
	// Source order is deliberately not execution order.
	r := Compute(fixture(), changes("X", "D", "Route", "B", "A"))

	if !r.EntrypointsKnown {
		t.Fatal("entrypoints are known for this engine")
	}
	// Main and Route each reach three changes (Main: A, B, D; Route: Route,
	// B, D), so source order of the entrypoints breaks the tie: Main first,
	// then its callees in call order, then Route; X closes the list.
	want := []string{"pkg.A", "pkg.B", "pkg.D", "pkg.Route", "pkg.X"}
	if got := symbols(r.Functions); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if r.Unreached != 1 {
		t.Errorf("unreached = %d, want 1 (X is only referenced, never called)", r.Unreached)
	}

	byName := map[string]Function{}
	for _, f := range r.Functions {
		byName[f.Symbol] = f
	}

	// B is one hop from Route and two from Main, so Route is its nearest.
	b := byName["pkg.B"]
	if len(b.Routes) != 2 || b.Routes[0].ID != id("Route") || b.Routes[1].ID != id("Main") {
		t.Fatalf("B routes = %+v, want Route then Main", b.Routes)
	}
	if got := b.Routes[0].Path; !reflect.DeepEqual(got, []model.TargetID{id("Route"), id("B")}) {
		t.Errorf("B nearest path = %v", got)
	}
	if got := b.Routes[1].Path; !reflect.DeepEqual(got, []model.TargetID{id("Main"), id("A"), id("B")}) {
		t.Errorf("B path from Main = %v", got)
	}

	// Expand follows nearest paths: Route opens B and B > D; B opens D; A
	// opens nothing, since nobody's nearest path runs through it.
	if got := byName["pkg.Route"].Expand; !reflect.DeepEqual(got, []string{"pkg.B", "pkg.B > pkg.D"}) {
		t.Errorf("Route expand = %v", got)
	}
	if got := byName["pkg.B"].Expand; !reflect.DeepEqual(got, []string{"pkg.D"}) {
		t.Errorf("B expand = %v", got)
	}
	if got := byName["pkg.A"].Expand; len(got) != 0 {
		t.Errorf("A expand = %v, want none", got)
	}

	// An entrypoint that is itself changed reaches itself by a path of one.
	rt := byName["pkg.Route"]
	if len(rt.Routes) == 0 || len(rt.Routes[0].Path) != 1 || rt.Routes[0].Kind != "http.route" {
		t.Errorf("Route's own route = %+v", rt.Routes)
	}
	if x := byName["pkg.X"]; len(x.Routes) != 0 {
		t.Errorf("X should be unreached, got %+v", x.Routes)
	}
	if r.Names[id("A")] != "pkg.A" || r.Names[id("Main")] != "pkg.Main" {
		t.Errorf("names missing path hops: %v", r.Names)
	}
}

func TestComputeWithoutEntrypoints(t *testing.T) {
	r := Compute(noEntrypoints{fixture()}, changes("D", "A"))
	if r.EntrypointsKnown {
		t.Fatal("engine can't list entrypoints")
	}
	if got := symbols(r.Functions); !reflect.DeepEqual(got, []string{"pkg.D", "pkg.A"}) {
		t.Errorf("order should be source order, got %v", got)
	}
	if r.Unreached != 2 {
		t.Errorf("unreached = %d, want 2", r.Unreached)
	}
}

func TestShortName(t *testing.T) {
	cases := map[model.TargetID]string{
		"github.com/x/y/pkg.Func":        "pkg.Func",
		"(*github.com/x/y/pkg.T).M":      "pkg.T.M",
		"(github.com/x/y/pkg.T).M":       "pkg.T.M",
		"other::(*github.com/x/pkg.T).M": "pkg.T.M",
		"main.main":                      "main.main",
		"(*github.com/x/y/pkg.T).M[int]": "pkg.T.M[int]",
	}
	for in, want := range cases {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTour(t *testing.T) {
	r := Compute(fixture(), changes("X", "D", "Route", "B", "A"))
	tour := Tour(r, "main", "0123456789abcdef", 2)

	if tour.Name != "diff:main" || tour.Title != "Changes vs main" || !tour.Generated {
		t.Errorf("tour identity: %+v", tour)
	}
	for _, want := range []string{"5 functions", "merge-base 0123456789ab", "1 is not reached", "2 functions deleted"} {
		if !strings.Contains(tour.Description, want) {
			t.Errorf("description lacks %q: %s", want, tour.Description)
		}
	}
	if len(tour.Stops) != 5 || tour.Stops[0].Symbol != "pkg.A" {
		t.Fatalf("stops = %+v", tour.Stops)
	}
	stop := map[string]string{}
	for _, s := range tour.Stops {
		stop[s.Symbol] = s.Description
	}
	// B: nearest is Route (one hop), also from Main; opens D below.
	if d := stop["pkg.B"]; !strings.Contains(d, "Reached from entrypoint [[pkg.Route]] (http.route GET /r).") ||
		!strings.Contains(d, "Also from 1 other entrypoint: [[pkg.Main]].") ||
		!strings.Contains(d, "Opened below: the changed 1 function it leads to, [[pkg.D]].") {
		t.Errorf("B description: %s", d)
	}
	// D: via a hop.
	if d := stop["pkg.D"]; !strings.Contains(d, "[[pkg.Route]] (http.route GET /r) via [[pkg.B]].") {
		t.Errorf("D description: %s", d)
	}
	if d := stop["pkg.Route"]; !strings.Contains(d, "An entrypoint itself (http.route GET /r).") {
		t.Errorf("Route description: %s", d)
	}
	if d := stop["pkg.X"]; !strings.Contains(d, "Not reached from any recognized entrypoint.") {
		t.Errorf("X description: %s", d)
	}
	if tour.Stops[3].Symbol != "pkg.Route" || !reflect.DeepEqual(tour.Stops[3].Expand, []string{"pkg.B", "pkg.B > pkg.D"}) {
		t.Errorf("Route stop expand = %+v", tour.Stops[3])
	}
}
