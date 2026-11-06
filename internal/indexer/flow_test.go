package indexer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// flowFixture loads testdata/flow and returns the frame for one of its
// functions along with a Flow for the identifier `name` on the first line of
// the frame's source containing `lineText`.
type flowFixture struct {
	t   *testing.T
	idx *Indexer
}

func loadFlowFixture(t *testing.T) flowFixture {
	t.Helper()
	dir, err := filepath.Abs("testdata/flow")
	if err != nil {
		t.Fatal(err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return flowFixture{t: t, idx: idx}
}

func (f flowFixture) frame(fn string) *Frame {
	f.t.Helper()
	id, err := f.idx.LookupSymbol(fn)
	if err != nil {
		f.t.Fatalf("LookupSymbol(%s): %v", fn, err)
	}
	fr, err := f.idx.Frame(id)
	if err != nil {
		f.t.Fatalf("Frame(%s): %v", fn, err)
	}
	return fr
}

// follow runs Flow for the identifier `name` on the frame line containing
// lineText — the way a reader would, by clicking a particular spelling of it.
func (f flowFixture) follow(fn, lineText, name string) (*Frame, *model.Flow) {
	f.t.Helper()
	fr := f.frame(fn)
	off := offsetIn(f.t, fr.Source, lineText, name)
	flow, err := f.idx.Flow(fr.ID, off)
	if err != nil {
		f.t.Fatalf("Flow(%s %q): %v", fn, name, err)
	}
	if flow == nil {
		f.t.Fatalf("Flow(%s %q): nil", fn, name)
	}
	return fr, flow
}

func offsetIn(t *testing.T, src, lineText, name string) int {
	t.Helper()
	li := strings.Index(src, lineText)
	if li < 0 {
		t.Fatalf("line %q not in source", lineText)
	}
	ni := strings.Index(lineText, name)
	if ni < 0 {
		t.Fatalf("%q not in line %q", name, lineText)
	}
	return li + ni
}

// spanTexts renders spans as "<frame line idx>:<text>", the form the
// expectations below are written in, and checks each one lands on the text
// it claims to — a span that says `err` but covers `error` is the kind of
// off-by-one a column conversion produces.
func spanTexts(t *testing.T, fr *Frame, spans []model.FlowSpan) []string {
	t.Helper()
	lines := strings.Split(fr.Source, "\n")
	var out []string
	for _, sp := range spans {
		if sp.File != fr.File {
			t.Errorf("span in %s, frame is %s", sp.File, fr.File)
			continue
		}
		idx := sp.Line - fr.StartLine
		if idx < 0 || idx >= len(lines) {
			t.Errorf("span line %d outside frame %d-%d", sp.Line, fr.StartLine, fr.EndLine)
			continue
		}
		line := []rune(lines[idx])
		start, end := sp.Col-1, sp.Col-1+sp.Len
		if start < 0 || end > len(line) {
			t.Errorf("span col %d len %d outside line %q", sp.Col, sp.Len, lines[idx])
			continue
		}
		out = append(out, string(line[start:end]))
	}
	return out
}

func lineOf(fr *Frame, sp model.FlowSpan) string {
	lines := strings.Split(fr.Source, "\n")
	idx := sp.Line - fr.StartLine
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[idx])
}

func edgesOfKind(flow *model.Flow, kind string) []model.FlowEdge {
	var out []model.FlowEdge
	for _, e := range flow.Edges {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Following the outer err lights its three spellings and not the inner err's,
// and vice versa: identity is the object's, not the name's.
func TestFlowShadowing(t *testing.T) {
	f := loadFlowFixture(t)

	fr, outer := f.follow("Run", "greeting, err := greet", "err")
	if outer.Kind != "var" || outer.Type != "error" {
		t.Errorf("outer err: kind=%q type=%q", outer.Kind, outer.Type)
	}
	var outerLines []string
	for _, sp := range outer.Spans {
		outerLines = append(outerLines, lineOf(fr, sp))
	}
	want := []string{"greeting, err := greet(name, cfg.Count)", "if err != nil {", `return "", err`}
	if strings.Join(outerLines, "|") != strings.Join(want, "|") {
		t.Errorf("outer err spans on lines %q, want %q", outerLines, want)
	}
	for _, text := range spanTexts(t, fr, outer.Spans) {
		if text != "err" {
			t.Errorf("outer span text %q, want err", text)
		}
	}

	_, inner := f.follow("Run", "err := validate(greeting)", "err")
	var innerLines []string
	for _, sp := range inner.Spans {
		innerLines = append(innerLines, lineOf(fr, sp))
	}
	wantInner := []string{"err := validate(greeting)", "if err != nil {", "fmt.Println(err)"}
	if strings.Join(innerLines, "|") != strings.Join(wantInner, "|") {
		t.Errorf("inner err spans on lines %q, want %q", innerLines, wantInner)
	}
}

// An argument flows into the callee's parameter: the edge names the call
// site, the callee, the parameter, its spans in the callee, and seeds the
// callee frame at the parameter's definition.
func TestFlowArgToParam(t *testing.T) {
	f := loadFlowFixture(t)
	fr, flow := f.follow("Run", "greeting, err := greet(name", "name")
	if flow.Kind != "param" {
		t.Errorf("kind: got %q want param", flow.Kind)
	}
	if got := spanTexts(t, fr, flow.Spans); len(got) != 2 {
		t.Errorf("name spans: got %v want the parameter and its one use", got)
	}

	params := edgesOfKind(flow, "param")
	if len(params) != 1 {
		t.Fatalf("param edges: got %d (%+v)", len(params), flow.Edges)
	}
	e := params[0]
	if !strings.HasSuffix(string(e.TargetID), ".greet") || e.Index != 0 || e.Name != "who" {
		t.Errorf("edge: target=%s index=%d name=%q", e.TargetID, e.Index, e.Name)
	}
	if !strings.HasPrefix(string(e.CallID), string(fr.ID)+"@") {
		t.Errorf("edge call id %s is not a site in %s", e.CallID, fr.ID)
	}
	greet := f.frame("greet")
	texts := spanTexts(t, greet, e.Spans)
	if len(texts) != 2 {
		t.Errorf("who spans in greet: got %v", texts)
	}
	for _, s := range texts {
		if s != "who" {
			t.Errorf("param span text %q, want who", s)
		}
	}
	if e.Seed == nil || lineOf(greet, *e.Seed) != "func greet(who string, n int) (string, error) {" {
		t.Errorf("seed: got %+v, want the parameter's definition", e.Seed)
	}

	// And the other way: the parameter is fed by main's argument.
	args := edgesOfKind(flow, "arg")
	if len(args) != 1 {
		t.Fatalf("arg edges: got %d", len(args))
	}
	mainFr := f.frame("main")
	if args[0].Index != 1 || !strings.HasSuffix(string(args[0].TargetID), ".main") {
		t.Errorf("arg edge: %+v", args[0])
	}
	if got := spanTexts(t, mainFr, args[0].Spans); len(got) != 1 || got[0] != "cfg.Name" {
		t.Errorf("arg span: got %v want [cfg.Name]", got)
	}
	// A field selection seeds at the field name, which is what the far
	// frame can locate.
	if args[0].Seed == nil || spanTexts(t, mainFr, []model.FlowSpan{*args[0].Seed})[0] != "Name" {
		t.Errorf("arg seed: got %+v", args[0].Seed)
	}
}

// A received result traces back to what the callee returns in that position,
// seeded at the returned variable so the callee frame can keep following.
func TestFlowResultToReturn(t *testing.T) {
	f := loadFlowFixture(t)
	_, flow := f.follow("Run", "greeting, err := greet", "greeting")

	results := edgesOfKind(flow, "result")
	if len(results) != 1 {
		t.Fatalf("result edges: got %d (%+v)", len(results), flow.Edges)
	}
	e := results[0]
	greet := f.frame("greet")
	if e.Index != 0 || !strings.HasSuffix(string(e.TargetID), ".greet") {
		t.Errorf("edge: %+v", e)
	}
	texts := spanTexts(t, greet, e.Spans)
	if strings.Join(texts, "|") != `""|msg` {
		t.Errorf(`return spans: got %v want ["" msg]`, texts)
	}
	if e.Seed == nil || spanTexts(t, greet, []model.FlowSpan{*e.Seed})[0] != "msg" {
		t.Errorf("seed: got %+v want msg", e.Seed)
	}

	// A named result is followed as the variable it is.
	_, outFlow := f.follow("Run", "out := decorate", "out")
	results = edgesOfKind(outFlow, "result")
	if len(results) != 1 {
		t.Fatalf("decorate result edges: got %d", len(results))
	}
	dec := f.frame("decorate")
	texts = spanTexts(t, dec, results[0].Spans)
	if len(texts) != 2 || texts[0] != "out" || texts[1] != "out" {
		t.Errorf("named result spans: got %v", texts)
	}
	if results[0].Seed == nil || lineOf(dec, *results[0].Seed) != "func decorate(s string) (out string) {" {
		t.Errorf("named result seed: %+v", results[0].Seed)
	}

	// From inside the callee, the returned value points back at the caller's
	// receiving name.
	_, msgFlow := f.follow("greet", "return msg, nil", "msg")
	rets := edgesOfKind(msgFlow, "ret")
	if len(rets) != 1 {
		t.Fatalf("ret edges: got %d (%+v)", len(rets), msgFlow.Edges)
	}
	run := f.frame("Run")
	if rets[0].Index != 0 || spanTexts(t, run, rets[0].Spans)[0] != "greeting" {
		t.Errorf("ret edge: %+v", rets[0])
	}
}

// A field read through a variable is followed as that variable's field, and
// flows into a callee like any other argument.
func TestFlowStructField(t *testing.T) {
	f := loadFlowFixture(t)
	fr, flow := f.follow("Run", "greet(name, cfg.Count)", "Count")
	if flow.Kind != "field" || flow.Type != "int" {
		t.Errorf("field: kind=%q type=%q", flow.Kind, flow.Type)
	}
	texts := spanTexts(t, fr, flow.Spans)
	if len(texts) != 2 || texts[0] != "Count" || texts[1] != "Count" {
		t.Errorf("field spans: got %v want both cfg.Count selections", texts)
	}
	params := edgesOfKind(flow, "param")
	if len(params) != 1 || params[0].Name != "n" || params[0].Index != 1 {
		t.Fatalf("param edge for the field: %+v", params)
	}
	greet := f.frame("greet")
	if got := spanTexts(t, greet, params[0].Spans); len(got) != 3 {
		t.Errorf("n spans in greet: got %v", got)
	}
}

// Things that aren't values answer nil rather than an error.
func TestFlowIgnoresNonValues(t *testing.T) {
	f := loadFlowFixture(t)
	fr := f.frame("Run")
	for _, c := range []struct{ line, name string }{
		{"err := greet(name", "greet"},    // a function
		{"func Run(cfg Config", "Config"}, // a type
		{"fmt.Println(err)", "fmt"},       // a package
	} {
		flow, err := f.idx.Flow(fr.ID, offsetIn(t, fr.Source, c.line, c.name))
		if err != nil || flow != nil {
			t.Errorf("Flow over %q: got %+v, %v; want nil, nil", c.name, flow, err)
		}
	}
}
