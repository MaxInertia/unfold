package indexer

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
	"golang.org/x/tools/go/ast/astutil"
)

// Bounds on the work one Flow request does. The reader has a handful of
// frames open; a parameter of a function with two hundred callers is followed
// into the first few of them, and an interface call with a dozen
// implementations into the first few, because what matters is the frames on
// screen and those are never all of them. Each caller costs one walk of its
// body, so this is also what keeps a request to roughly one function's worth
// of AST plus its direct neighbours.
const (
	maxFlowCallers    = 24
	maxFlowCandidates = 8
)

// Flow answers "where does this value go" for the identifier at a UTF-16
// offset into the frame id's Source: every place the same object is defined
// or used in its function, plus the edges that carry it into the functions
// at the other end of its call sites (see model.FlowEdge).
//
// Identity is the type checker's: two variables spelled `err` in nested
// scopes are two *types.Var, so following the outer one never lights the
// inner one. Only values are followed — a function name, a type or a package
// under the pointer answers nil, as does an identifier outside any function,
// because "every use of a package-level variable" is a usages question, not a
// path through one function.
func (i *Indexer) Flow(id TargetID, offset int) (*model.Flow, error) {
	loc, err := i.locate(id, offset)
	if loc == nil || err != nil {
		return nil, err
	}
	info := loc.pkg.TypesInfo
	obj, ok := info.ObjectOf(loc.ident).(*types.Var)
	if !ok {
		return nil, nil
	}

	// The function the identifier is in. A function frame is that function;
	// a whole-file frame has to find it from the enclosing path, and then the
	// index entry for it, which is what knows its call sites.
	fi, scope := loc.fi, ast.Node(nil)
	if fi != nil {
		scope = fi.node
	} else {
		for _, n := range loc.enclosing {
			if fd, ok := n.(*ast.FuncDecl); ok {
				scope = fd
				if fn, ok := info.Defs[fd.Name].(*types.Func); ok {
					fi = i.fn(TargetID(fn.FullName()))
				}
				break
			}
		}
	}
	if scope == nil {
		return nil, nil
	}

	t := tracked{obj: obj, info: info}
	if obj.IsField() {
		// A field is a value only when selected from something. The same
		// name in a struct type or as a composite-literal key is a slot, and
		// following it would mean something different from following `s.cfg`.
		sel := selectorOf(loc.enclosing, loc.ident)
		if sel == nil {
			return nil, nil
		}
		if x, ok := unwrapValue(sel.X).(*ast.Ident); ok {
			if base, ok := info.ObjectOf(x).(*types.Var); ok {
				t.base = base
			}
		}
	}

	flow := &model.Flow{
		Name:  loc.ident.Name,
		Kind:  "var",
		Type:  types.TypeString(obj.Type(), types.RelativeTo(loc.pkg.Types)),
		Spans: i.spansIn(t, scope),
		Edges: []model.FlowEdge{},
	}
	if obj.IsField() {
		flow.Kind = "field"
	}
	if fi != nil {
		if idx, variadic, isParam := paramIndexOf(fi, obj); isParam {
			flow.Kind = "param"
			flow.Edges = append(flow.Edges, i.argEdges(fi, idx, variadic)...)
		}
		if sig := funcSig(fi); sig != nil {
			for k := 0; k < sig.Results().Len(); k++ {
				if sig.Results().At(k) == obj {
					flow.Kind = "result"
				}
			}
		}
		flow.Edges = append(flow.Edges, i.callEdges(fi, scope, t)...)
		flow.Edges = append(flow.Edges, i.retEdges(fi, t)...)
	}
	sortSpans(flow.Spans)
	for n := range flow.Edges {
		sortSpans(flow.Edges[n].Spans)
	}
	return flow, nil
}

// tracked is the value being followed. A plain variable is matched by object
// identity, which is what makes shadowing work. A struct field is matched by
// the field object together with the variable it was selected from, when that
// is a plain name — `s.cfg` and `other.cfg` are the same field but not the
// same value. `a.b.cfg` has no single base and falls back to the field alone.
type tracked struct {
	obj  *types.Var
	base *types.Var // the variable a followed field is selected from, or nil
	info *types.Info
}

// matches reports whether expr denotes the tracked value. &x, *x and (x) all
// count: it is the same value being addressed or read through, and the
// callee that receives it is where the reader wants to go next.
func (t tracked) matches(expr ast.Expr) bool {
	switch e := unwrapValue(expr).(type) {
	case *ast.Ident:
		return !t.obj.IsField() && t.info.ObjectOf(e) == t.obj
	case *ast.SelectorExpr:
		return t.matchesSelector(e)
	}
	return false
}

func (t tracked) matchesSelector(sel *ast.SelectorExpr) bool {
	if !t.obj.IsField() || t.info.Uses[sel.Sel] != t.obj {
		return false
	}
	if t.base == nil {
		return true
	}
	x, ok := unwrapValue(sel.X).(*ast.Ident)
	return ok && t.info.ObjectOf(x) == t.base
}

func unwrapValue(expr ast.Expr) ast.Expr {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		case *ast.UnaryExpr:
			if e.Op != token.AND {
				return expr
			}
			expr = e.X
		default:
			return expr
		}
	}
}

func unwrapParens(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = p.X
	}
}

// selectorOf returns the selector whose field name is ident, or nil when the
// identifier isn't the Sel of one.
func selectorOf(enclosing []ast.Node, ident *ast.Ident) *ast.SelectorExpr {
	for _, n := range enclosing {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if sel.Sel == ident {
				return sel
			}
			return nil
		}
	}
	return nil
}

// spansIn collects every place the tracked value is named inside node, as the
// token that names it: the variable, or the field name of a selector.
// Closures are walked too — a captured variable is the same value.
func (i *Indexer) spansIn(t tracked, node ast.Node) []model.FlowSpan {
	var out []model.FlowSpan
	ast.Inspect(node, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.Ident:
			if !t.obj.IsField() && (t.info.Defs[e] == t.obj || t.info.Uses[e] == t.obj) {
				if sp, ok := i.spanOf(e.Pos(), e.End()); ok {
					out = addSpan(out, sp)
				}
			}
		case *ast.SelectorExpr:
			if t.matchesSelector(e) {
				if sp, ok := i.spanOf(e.Sel.Pos(), e.Sel.End()); ok {
					out = addSpan(out, sp)
				}
			}
		}
		return true
	})
	return out
}

// defSpan is where the tracked variable is declared inside node, or nil.
func (i *Indexer) defSpan(t tracked, node ast.Node) *model.FlowSpan {
	var found *model.FlowSpan
	ast.Inspect(node, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if e, ok := n.(*ast.Ident); ok && t.info.Defs[e] == t.obj {
			if sp, ok := i.spanOf(e.Pos(), e.End()); ok {
				found = &sp
			}
		}
		return true
	})
	return found
}

// spanOf converts a source range to a FlowSpan on its first line. Columns
// come out in UTF-16 units — the frontend indexes a JavaScript string — and a
// range that runs past the end of its line is cut there, since a span is a
// mark on one line and a multi-line return expression is marked where it
// starts.
func (i *Indexer) spanOf(pos, end token.Pos) (model.FlowSpan, bool) {
	p := i.fset.Position(pos)
	e := i.fset.Position(end)
	if !p.IsValid() {
		return model.FlowSpan{}, false
	}
	buf, err := i.readFile(p.Filename)
	if err != nil || p.Offset > len(buf) || p.Column < 1 {
		return model.FlowSpan{}, false
	}
	lineStart := p.Offset - (p.Column - 1)
	stop := e.Offset
	if e.Line != p.Line || stop > len(buf) || stop < p.Offset {
		stop = p.Offset
		for stop < len(buf) && buf[stop] != '\n' {
			stop++
		}
	}
	return model.FlowSpan{
		File: p.Filename,
		Line: p.Line,
		Col:  utf16Offset(buf[lineStart:p.Offset], p.Offset-lineStart) + 1,
		Len:  utf16Offset(buf[p.Offset:stop], stop-p.Offset),
	}, true
}

func addSpan(list []model.FlowSpan, sp model.FlowSpan) []model.FlowSpan {
	for _, have := range list {
		if have == sp {
			return list
		}
	}
	return append(list, sp)
}

func sortSpans(spans []model.FlowSpan) {
	sort.Slice(spans, func(a, b int) bool {
		if spans[a].Line != spans[b].Line {
			return spans[a].Line < spans[b].Line
		}
		return spans[a].Col < spans[b].Col
	})
}

// fn is the index entry for a target, read under the lock.
func (i *Indexer) fn(id TargetID) *funcInfo {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.funcs[id]
}

func funcSig(fi *funcInfo) *types.Signature {
	if fi == nil || fi.obj == nil {
		return nil
	}
	sig, _ := fi.obj.Type().(*types.Signature)
	return sig
}

// paramIndexOf reports which parameter of fi obj is: its flat index, whether
// it is the variadic one, and -1 for the receiver. ok is false when obj is
// not a parameter of fi at all.
func paramIndexOf(fi *funcInfo, obj *types.Var) (idx int, variadic, ok bool) {
	if fi.decl == nil {
		return 0, false, false
	}
	info := fi.pkg.TypesInfo
	if fi.decl.Recv != nil {
		for _, f := range fi.decl.Recv.List {
			for _, n := range f.Names {
				if info.Defs[n] == obj {
					return -1, false, true
				}
			}
		}
	}
	for _, f := range fi.decl.Type.Params.List {
		_, isVariadic := f.Type.(*ast.Ellipsis)
		if len(f.Names) == 0 {
			idx++
			continue
		}
		for _, n := range f.Names {
			if info.Defs[n] == obj {
				return idx, isVariadic, true
			}
			idx++
		}
	}
	return 0, false, false
}

// callee is one indexed function a call site may reach.
type callee struct {
	call *callInfo
	fi   *funcInfo
}

// calleesOf resolves a call inside parent to the indexed functions it may
// reach: the one target of a direct call, or each candidate of an interface
// call (bounded). The call is matched to its index entry by the position of
// its name token, which is how the index spans it.
func (i *Indexer) calleesOf(parent *funcInfo, ce *ast.CallExpr) []callee {
	pos, _, ok := nameSpan(ce.Fun)
	if !ok {
		return nil
	}
	var ci *callInfo
	for _, c := range parent.calls {
		if c.pos == pos && c.kind != KindRef {
			ci = c
			break
		}
	}
	if ci == nil {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	var out []callee
	switch ci.kind {
	case KindDirect:
		if fi := i.funcs[ci.target]; fi != nil {
			out = append(out, callee{ci, fi})
		}
	case KindInterface:
		for n, cand := range ci.candidates {
			if n == maxFlowCandidates {
				break
			}
			if fi := i.funcs[cand.TargetID]; fi != nil {
				out = append(out, callee{ci, fi})
			}
		}
	}
	return out
}

// callEdges finds the calls in scope that the tracked value is handed to
// (as receiver or argument) and the calls it receives a result from (`x, err
// := f()`, `var x = f()`), and resolves each to the far side.
func (i *Indexer) callEdges(fi *funcInfo, scope ast.Node, t tracked) []model.FlowEdge {
	var out []model.FlowEdge
	ast.Inspect(scope, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.CallExpr:
			var callees []callee
			resolved := false
			get := func() []callee {
				if !resolved {
					callees, resolved = i.calleesOf(fi, s), true
				}
				return callees
			}
			if sel, ok := s.Fun.(*ast.SelectorExpr); ok && t.matches(sel.X) {
				for _, c := range get() {
					if c.fi.decl != nil && c.fi.decl.Recv != nil {
						if e, ok := i.paramEdge(c, -1); ok {
							out = append(out, e)
						}
					}
				}
			}
			for ai, arg := range s.Args {
				if !t.matches(arg) {
					continue
				}
				for _, c := range get() {
					if e, ok := i.paramEdge(c, ai); ok {
						out = append(out, e)
					}
				}
			}
		case *ast.AssignStmt:
			if len(s.Rhs) != 1 {
				return true
			}
			ce, ok := unwrapParens(s.Rhs[0]).(*ast.CallExpr)
			if !ok {
				return true
			}
			for k, lhs := range s.Lhs {
				if !t.matches(lhs) {
					continue
				}
				for _, c := range i.calleesOf(fi, ce) {
					if e, ok := i.resultEdge(c, k); ok {
						out = append(out, e)
					}
				}
			}
		case *ast.ValueSpec:
			if len(s.Values) != 1 {
				return true
			}
			ce, ok := unwrapParens(s.Values[0]).(*ast.CallExpr)
			if !ok {
				return true
			}
			for k, name := range s.Names {
				if !t.matches(name) {
					continue
				}
				for _, c := range i.calleesOf(fi, ce) {
					if e, ok := i.resultEdge(c, k); ok {
						out = append(out, e)
					}
				}
			}
		}
		return true
	})
	return out
}

// paramEdge is the edge from an argument (or receiver, argIndex -1) into the
// callee's corresponding parameter: its definition and uses in the callee,
// seeded at the definition so the callee frame can carry on from there. An
// argument past the fixed parameters of a variadic function lands in the
// variadic one.
func (i *Indexer) paramEdge(c callee, argIndex int) (model.FlowEdge, bool) {
	sig := funcSig(c.fi)
	if sig == nil {
		return model.FlowEdge{}, false
	}
	var pv *types.Var
	if argIndex < 0 {
		pv = sig.Recv()
	} else {
		params := sig.Params()
		pi := argIndex
		if sig.Variadic() && pi >= params.Len()-1 {
			pi = params.Len() - 1
		}
		if pi < 0 || pi >= params.Len() {
			return model.FlowEdge{}, false
		}
		pv = params.At(pi)
	}
	if pv == nil {
		return model.FlowEdge{}, false
	}
	edge := model.FlowEdge{
		Kind:     "param",
		CallID:   c.call.id,
		TargetID: c.fi.id,
		Index:    argIndex,
		Name:     pv.Name(),
		Spans:    []model.FlowSpan{},
	}
	// An unnamed or blank parameter receives the value and never reads it:
	// the edge exists, there is just nothing to light up.
	if pv.Name() == "" || pv.Name() == "_" {
		return edge, true
	}
	t := tracked{obj: pv, info: c.fi.pkg.TypesInfo}
	edge.Spans = i.spansIn(t, c.fi.node)
	edge.Seed = i.defSpan(t, c.fi.node)
	return edge, true
}

// resultEdge is the edge from a receiving variable (`x` in `x, err := f()`)
// to what the callee returns in that position: every return statement's k-th
// expression, and — when the result is named — the result variable's own
// definition and uses. The seed is the result variable, or failing that the
// first returned expression that is a plain variable.
func (i *Indexer) resultEdge(c callee, k int) (model.FlowEdge, bool) {
	sig := funcSig(c.fi)
	if sig == nil || k >= sig.Results().Len() {
		return model.FlowEdge{}, false
	}
	rv := sig.Results().At(k)
	info := c.fi.pkg.TypesInfo
	edge := model.FlowEdge{
		Kind:     "result",
		CallID:   c.call.id,
		TargetID: c.fi.id,
		Index:    k,
		Name:     rv.Name(),
		Spans:    []model.FlowSpan{},
	}
	if rv.Name() != "" && rv.Name() != "_" {
		t := tracked{obj: rv, info: info}
		edge.Spans = i.spansIn(t, c.fi.node)
		edge.Seed = i.defSpan(t, c.fi.node)
	}
	n := sig.Results().Len()
	forEachReturn(c.fi.node, func(rs *ast.ReturnStmt) {
		var expr ast.Expr
		switch {
		case len(rs.Results) == n:
			expr = rs.Results[k]
		case len(rs.Results) == 1 && n > 1:
			// `return g()` hands back g's whole tuple; the k-th value is
			// somewhere inside that call, so the call is what gets marked.
			expr = rs.Results[0]
		default:
			return // a bare return of named results, covered above
		}
		i.addExprSpan(&edge, expr, info)
	})
	return edge, true
}

// addExprSpan marks an expression on an edge and, when the expression is a
// plain variable or a field selection, offers it as the seed to keep
// following from — unless the edge already has one.
func (i *Indexer) addExprSpan(edge *model.FlowEdge, expr ast.Expr, info *types.Info) {
	sp, ok := i.spanOf(expr.Pos(), expr.End())
	if !ok {
		return
	}
	edge.Spans = addSpan(edge.Spans, sp)
	if edge.Seed != nil {
		return
	}
	var name *ast.Ident
	switch e := unwrapValue(expr).(type) {
	case *ast.Ident:
		name = e
	case *ast.SelectorExpr:
		name = e.Sel
	}
	if name == nil {
		return
	}
	if _, isVar := info.ObjectOf(name).(*types.Var); !isVar {
		return
	}
	if seed, ok := i.spanOf(name.Pos(), name.End()); ok {
		edge.Seed = &seed
	}
}

// forEachReturn visits the return statements of a function body, not those
// of closures inside it — a closure's return hands a value to whoever calls
// the closure, which is not the function being read.
func forEachReturn(node ast.Node, fn func(*ast.ReturnStmt)) {
	ast.Inspect(node, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			fn(s)
		}
		return true
	})
}

// callerSite is one call site that reaches a target: the caller, the index's
// entry for the site, and the call expression itself.
type callerSite struct {
	fi   *funcInfo
	call *callInfo
	ce   *ast.CallExpr
}

// callerSites lists the (bounded) call sites that reach target, found through
// the usages index. Value references are skipped: nothing is passed or
// returned where a function is merely named.
func (i *Indexer) callerSites(target TargetID) []callerSite {
	i.mu.RLock()
	usages := i.usagesByTarget[target]
	i.mu.RUnlock()
	var out []callerSite
	for _, u := range usages {
		if u.call == nil || u.kind == model.UsageRef {
			continue
		}
		caller := i.fn(u.parent)
		if caller == nil {
			continue
		}
		ce := callExprAt(caller.node, u.call.pos)
		if ce == nil {
			continue
		}
		out = append(out, callerSite{fi: caller, call: u.call, ce: ce})
		if len(out) == maxFlowCallers {
			break
		}
	}
	return out
}

// callExprAt finds the call inside node whose name token starts at namePos —
// the position the index recorded for the site.
func callExprAt(node ast.Node, namePos token.Pos) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if ce, ok := n.(*ast.CallExpr); ok {
			if p, _, ok := nameSpan(ce.Fun); ok && p == namePos {
				found = ce
				return false
			}
		}
		return true
	})
	return found
}

// argEdges are the edges from a parameter of fi back to what each caller
// passes in that position: the receiver expression for idx -1, every trailing
// argument for the variadic parameter, otherwise the one argument.
func (i *Indexer) argEdges(fi *funcInfo, idx int, variadic bool) []model.FlowEdge {
	var out []model.FlowEdge
	for _, cs := range i.callerSites(fi.id) {
		var exprs []ast.Expr
		switch {
		case idx < 0:
			if sel, ok := cs.ce.Fun.(*ast.SelectorExpr); ok {
				exprs = []ast.Expr{sel.X}
			}
		case variadic && idx < len(cs.ce.Args):
			exprs = cs.ce.Args[idx:]
		case idx < len(cs.ce.Args):
			exprs = cs.ce.Args[idx : idx+1]
		}
		if len(exprs) == 0 {
			continue
		}
		edge := model.FlowEdge{
			Kind:     "arg",
			CallID:   cs.call.id,
			TargetID: cs.fi.id,
			Index:    idx,
			Spans:    []model.FlowSpan{},
		}
		for _, e := range exprs {
			i.addExprSpan(&edge, e, cs.fi.pkg.TypesInfo)
		}
		if len(edge.Spans) > 0 {
			out = append(out, edge)
		}
	}
	return out
}

// retEdges are the edges from a value fi returns — a named result, or
// anything appearing in a return statement — to what receives it at each
// caller: the k-th name on the left of `x, err := f()` or `var x = f()`. A
// caller that uses the call some other way (`if f() != nil`, `g(f())`) has
// no single name to light up and gets no edge.
func (i *Indexer) retEdges(fi *funcInfo, t tracked) []model.FlowEdge {
	sig := funcSig(fi)
	if sig == nil {
		return nil
	}
	n := sig.Results().Len()
	indexes := map[int]bool{}
	for k := 0; k < n; k++ {
		if sig.Results().At(k) == t.obj {
			indexes[k] = true
		}
	}
	forEachReturn(fi.node, func(rs *ast.ReturnStmt) {
		if len(rs.Results) != n {
			return
		}
		for k, e := range rs.Results {
			if t.matches(e) {
				indexes[k] = true
			}
		}
	})
	if len(indexes) == 0 {
		return nil
	}
	ks := make([]int, 0, len(indexes))
	for k := range indexes {
		ks = append(ks, k)
	}
	sort.Ints(ks)

	var out []model.FlowEdge
	callers := i.callerSites(fi.id)
	for _, k := range ks {
		for _, cs := range callers {
			lhs := receiverOf(cs, k)
			if lhs == nil {
				continue
			}
			edge := model.FlowEdge{
				Kind:     "ret",
				CallID:   cs.call.id,
				TargetID: cs.fi.id,
				Index:    k,
				Spans:    []model.FlowSpan{},
			}
			i.addExprSpan(&edge, lhs, cs.fi.pkg.TypesInfo)
			if len(edge.Spans) > 0 {
				out = append(out, edge)
			}
		}
	}
	return out
}

// receiverOf is the expression that receives result k of the call at cs:
// the k-th left-hand side of an assignment or var declaration whose only
// right-hand side is the call. Nil when the call's results go anywhere else.
func receiverOf(cs callerSite, k int) ast.Expr {
	file := fileContaining(cs.fi.node, cs.fi.pkg)
	if file == nil {
		return nil
	}
	path, _ := astutil.PathEnclosingInterval(file, cs.ce.Pos(), cs.ce.End())
	for _, n := range path {
		switch s := n.(type) {
		case *ast.CallExpr, *ast.ParenExpr:
			continue
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 && unwrapParens(s.Rhs[0]) == ast.Expr(cs.ce) && k < len(s.Lhs) {
				return s.Lhs[k]
			}
			return nil
		case *ast.ValueSpec:
			if len(s.Values) == 1 && unwrapParens(s.Values[0]) == ast.Expr(cs.ce) && k < len(s.Names) {
				return s.Names[k]
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}
