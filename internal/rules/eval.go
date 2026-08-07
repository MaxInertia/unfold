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
	callee map[callKey]model.TargetID

	// marks[rule][fn] is the key that rule matched inside fn's body.
	marks map[string]map[model.TargetID]string
	// Stats counts what each rule matched, so a rule that has quietly stopped
	// matching after a library upgrade can say so instead of contributing
	// nothing in silence.
	Stats map[string]int
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
		callee: map[callKey]model.TargetID{},
		marks:  map[string]map[model.TargetID]string{},
		Stats:  map[string]int{},
	}
	for _, r := range rs {
		if !r.On() || r.Match == nil || r.Emit == nil {
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

// Observe records one call site, attributed to the function containing it.
// Called once per call site during indexing, before Run.
func (e *Evaluator) Observe(c platform.Call, target model.TargetID) {
	if len(e.rules) == 0 {
		return
	}
	e.calls[c.Site] = append(e.calls[c.Site], c)
	if target != "" {
		e.callee[keyOf(c)] = target
	}
}

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
				if b, ok := e.apply(r, c); ok {
					e.Stats[r.ID]++
					out = append(out, b)
				}
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
					t, ok := e.callee[keyOf(c)]
					if !ok {
						continue
					}
					if key, marked := marks[t]; marked {
						marks[fn] = key
						added = true
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
		target, ok := e.callee[keyOf(c)]
		if !ok {
			return model.Binding{}, false // unresolved callee: nothing to inspect
		}
		key, marked := e.marks[n.Rule][target]
		if !marked {
			return model.Binding{}, false
		}
		inner = key
	}

	key, ok := expandKey(r.Emit.Key, c, caps, inner)
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
