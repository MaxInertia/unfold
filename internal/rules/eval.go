package rules

import (
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/platform"
)

// Evaluator applies a rule set to a whole project in two phases.
//
// Phase 1 classifies *functions* by what their own bodies do: "this function
// is a client for key K". Phase 2 classifies *call sites* whose callee carries
// such a classification. That split is what recognizes a wrapper — an SDK
// method is interesting because of what it does inside, not because of its
// name — and it is exactly the shape the built-in gRPC pass arrived at by
// hand, where invokePath/invokeCache is phase 1 and walking back to the owned
// caller is phase 2.
//
// The evaluator never sees an AST. It works on platform.Call facts the indexer
// already computes per call site, which is why adding rules costs no new
// traversal and why a second language engine could feed the same set.
type Evaluator struct {
	rules   []Rule
	byID    map[string]Rule
	// calls holds every call site in a function's own body, keyed by the
	// function. Phase 1 asks questions of this; it is the thing the indexer
	// used to compute for binding extraction and then throw away.
	calls map[model.TargetID][]platform.Call
	// callee maps a call site to the function it calls, so phase 2 can ask
	// what that function was classified as. Empty for unresolved calls.
	callee map[callKey][]model.TargetID

	// marks[rule][fn] is the key that rule matched inside fn's body.
	marks map[string]map[model.TargetID]string
	leaves map[string]LeafDecision
	// Stats counts what each rule matched, so a rule that has quietly stopped
	// matching after a library upgrade can say so instead of contributing
	// nothing in silence.
	Stats map[string]int
	// matched is Stats at the resolution the reader needs: which rules claimed
	// *this* call site, keyed by file:line. A count answers "is this rule
	// alive"; standing on a call and asking "what already recognizes this" is
	// a different question, and the one that stops you writing a rule you
	// already have.
	matched map[string][]string
}

type callKey struct {
	site model.TargetID
	file string
	line int
	fn   string
}

func keyOf(c platform.Call) callKey {
	return callKey{site: c.Site, file: c.File, line: c.Line, fn: c.Func}
}

// NewEvaluator builds an evaluator over the enabled rules.
func NewEvaluator(rs []Rule) *Evaluator {
	e := &Evaluator{
		byID:   map[string]Rule{},
		calls:  map[model.TargetID][]platform.Call{},
		callee: map[callKey][]model.TargetID{},
		marks:   map[string]map[model.TargetID]string{},
		leaves:  map[string]LeafDecision{},
		Stats:   map[string]int{},
		matched: map[string][]string{},
	}
	for _, r := range rs {
		if !r.On() || r.Match == nil || (r.Emit == nil && r.Leaf == nil) {
			continue
		}
		e.rules = append(e.rules, r)
		e.byID[r.ID] = r
	}
	// Stable order so the emitted surface doesn't depend on map iteration —
	// the same reason binding order was pinned after it made a test a coin
	// flip.
	sort.Slice(e.rules, func(a, b int) bool { return e.rules[a].ID < e.rules[b].ID })
	return e
}

// Observe records one call site, attributed to the function containing it,
// along with every function it might call.
//
// Plural, because an interface call has no single callee: a client obtained as
// `NewReportingClient() ReportingClient` dispatches through the interface, and
// insisting on a direct target would miss exactly the calls that go through
// the abstraction a real SDK hands you.
func (e *Evaluator) Observe(c platform.Call, targets ...model.TargetID) {
	if len(e.rules) == 0 {
		return
	}
	e.calls[c.Site] = append(e.calls[c.Site], c)
	var keep []model.TargetID
	for _, t := range targets {
		if t != "" {
			keep = append(keep, t)
		}
	}
	if len(keep) > 0 {
		e.callee[keyOf(c)] = keep
	}
}

// LeafDecision is how a rule wants one call site treated when reading.
type LeafDecision struct {
	RuleID    string
	Expand    *bool
	Label     string
	CrossRepo bool
	// Key is the emitted key when the same rule also produced a binding, so
	// the leaf can offer the far end of that edge rather than just naming it.
	Key string
}

// Leaves returns the reading-time classification for call sites, keyed by the
// call site's file:line. Populated by Run.
func (e *Evaluator) Leaves() map[string]LeafDecision { return e.leaves }

// Matched returns the rules that claimed each call site, keyed by file:line
// like Leaves. Populated by Run.
func (e *Evaluator) Matched() map[string][]string { return e.matched }

// Run evaluates every rule and returns the bindings they imply.
//
// Attribution, reachability filtering and dedup are deliberately *not* done
// here: the engine that owns those walks applies them to whatever this
// returns, uniformly for built-in and configured rules alike. A rule says what
// shape counts; it does not get to re-specify the parts that were hardest to
// get right.
func (e *Evaluator) Run() []model.Binding {
	if len(e.rules) == 0 {
		return nil
	}
	e.runPhase1()

	var out []model.Binding
	sites := make([]model.TargetID, 0, len(e.calls))
	for fn := range e.calls {
		sites = append(sites, fn)
	}
	sort.Slice(sites, func(a, b int) bool { return sites[a] < sites[b] })

	for _, fn := range sites {
		for _, c := range e.calls[fn] {
			for _, r := range e.rules {
				if r.Classifier {
					continue // marks functions only; never an edge itself
				}
				b, ok := e.apply(r, c)
				if !ok {
					continue
				}
				if r.Leaf != nil {
					e.leaves[siteLine(c)] = LeafDecision{
						RuleID: r.ID, Expand: r.Leaf.Expand,
						Label: r.Leaf.Label, CrossRepo: r.Leaf.CrossRepo, Key: b.Key,
					}
				}
				// Recorded for every match, including the leaf-only rules that
				// emit nothing: "this call is a boundary because of rule X" is
				// exactly the kind of claim you want to see before writing
				// another rule over the same call.
				e.matched[siteLine(c)] = append(e.matched[siteLine(c)], r.ID)
				if r.Emit == nil {
					e.Stats[r.ID]++
					continue // leaf-only rule: classified, but not an edge
				}
				e.Stats[r.ID]++
				b.Rule = r.ID
				out = append(out, b)
			}
		}
	}
	return out
}

// runPhase1 marks, for every rule referenced by a calleeMatches clause, which
// functions contain a matching call in their own body — then widens by the
// configured depth.
func (e *Evaluator) runPhase1() {
	for _, r := range e.rules {
		n := r.Match.CalleeMatches
		if n == nil {
			continue
		}
		inner, ok := e.byID[n.Rule]
		if !ok || inner.Match == nil || inner.Emit == nil {
			continue // dangling reference; reported by Validate
		}
		if _, done := e.marks[n.Rule]; done {
			continue
		}
		marks := map[model.TargetID]string{}
		// Depth 1: the function's own body. This is the default and the safe
		// one — unbounded following is what produced edges for services that
		// were never called.
		for fn, calls := range e.calls {
			for _, c := range calls {
				caps := Captures{}
				if !inner.Match.Matches(c, caps) {
					continue
				}
				if key, ok := expandKey(inner.Emit.Key, c, caps, ""); ok {
					marks[fn] = key
					break
				}
			}
		}
		// Depth > 1 widens transitively: a function that calls something
		// already marked inherits the mark. Each round adds one hop, so the
		// bound stays exactly what the rule asked for rather than becoming
		// "everything eventually reachable".
		for d := 1; d < n.Depth; d++ {
			added := false
			for fn, calls := range e.calls {
				if _, has := marks[fn]; has {
					continue
				}
				for _, c := range calls {
					for _, t := range e.callee[keyOf(c)] {
						if key, marked := marks[t]; marked {
							marks[fn] = key
							added = true
							break
						}
					}
					if _, has := marks[fn]; has {
						break
					}
				}
			}
			if !added {
				break // fixpoint before the depth bound; nothing more to add
			}
		}
		e.marks[n.Rule] = marks
	}
}

// apply evaluates one rule against one call site.
func (e *Evaluator) apply(r Rule, c platform.Call) (model.Binding, bool) {
	caps := Captures{}
	if !r.Match.Matches(c, caps) {
		return model.Binding{}, false
	}

	inner := ""
	if n := r.Match.CalleeMatches; n != nil {
		found := false
		for _, target := range e.callee[keyOf(c)] {
			if key, marked := e.marks[n.Rule][target]; marked {
				inner, found = key, true
				break
			}
		}
		if !found {
			return model.Binding{}, false
		}
	}

	if r.Emit == nil {
		// Leaf-only: the match is the whole answer. A label may still want
		// the captures, so it's expanded here.
		label, _ := expandKey(r.Leaf.Label, c, caps, inner)
		return model.Binding{Key: label, Site: c.Site, File: c.File, Line: c.Line}, true
	}
	key, ok, inferredKey := expandKeyDetail(r.Emit.Key, c, caps, inner)
	if !ok || key == "" {
		// A key that can't be resolved is skipped rather than guessed at —
		// the same discipline as an outbound call whose URL is built at
		// runtime.
		return model.Binding{}, false
	}

	conf := model.BindingConfidence(r.Emit.Confidence)
	if conf == "" {
		conf = model.ConfDeclared
	}
	// A key read out of a variable's initializer is what the program starts
	// with, not what it necessarily uses. The rule's own confidence can't
	// override that: it describes the shape the author asserted, and this is
	// about the value, which they never saw.
	if inferredKey {
		conf = model.ConfInferred
	}
	b := model.Binding{
		Role:       model.BindingRole(r.Emit.Role),
		Kind:       r.Emit.Kind,
		Key:        key,
		Site:       c.Site,
		File:       c.File,
		Line:       c.Line,
		Confidence: conf,
		// Provenance names the rule, so a surprising edge is traceable to the
		// rule that produced it rather than looking like a tool bug.
		Detail: "rule " + r.ID,
	}
	if r.Emit.Handler != "" {
		if n, ok := argIndex(r.Emit.Handler); ok && n < len(c.Args) {
			b.Target = c.Args[n].Target
		}
	}
	return b, true
}

// Validate reports rules that can never fire — a dangling calleeMatches
// reference, or one pointing at a rule that is switched off. Both are silent
// failures otherwise: the rule simply never matches, and an empty surface
// looks like "nothing found" rather than "misconfigured".
func Validate(rs []Rule) []error {
	on := map[string]bool{}
	known := map[string]bool{}
	for _, r := range rs {
		known[r.ID] = true
		if r.On() {
			on[r.ID] = true
		}
	}
	var problems []error
	for _, r := range rs {
		if !r.On() || r.Match == nil || r.Match.CalleeMatches == nil {
			continue
		}
		ref := r.Match.CalleeMatches.Rule
		switch {
		case !known[ref]:
			problems = append(problems, errf("rule %q: calleeMatches names unknown rule %q", r.ID, ref))
		case !on[ref]:
			problems = append(problems, errf("rule %q: calleeMatches names %q, which is disabled — this rule can never match", r.ID, ref))
		}
	}
	return problems
}

// siteLine identifies a call site the way a Frame's call sites can be matched
// back to it — file and line, which is what the indexer knows at render time.
func siteLine(c platform.Call) string {
	return c.File + ":" + itoa(c.Line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
