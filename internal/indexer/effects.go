package indexer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
)

// Side effects: what a function touches outside its own stack, and which of
// its callees it touches it through.
//
// Two sources feed this. Call-shaped effects — a query, a socket, a file
// opened, an environment variable read — come from the rule evaluator, so the
// stdlib and the common libraries ship as rules in the same vocabulary a user
// adds their own with. Statement-shaped effects — a goroutine launched, a
// channel sent on, a package-level variable assigned — have no call site for a
// rule to match, so the indexer finds them in the AST directly, here.
//
// Both land in directEffects keyed by the function whose body holds them.
// Inheritance up the call graph is computed on demand per function, bounded by
// depth and budget, and cached for the life of the index: it is a question a
// frame header asks once per function, and a rebuild makes a new indexer.

const (
	// effectDepth is how many calls away evidence is still attributed to a
	// function. Four reaches a handler's service's repository's driver; past
	// that nearly everything touches nearly everything, and a badge that is
	// on every frame says nothing.
	effectDepth = 4
	// effectBudget caps the functions one walk visits, so a hub function
	// with a wide fan-out answers in bounded time. A truncated report says so.
	effectBudget = 250
)

// collectStmtEffects records the effects in fi's body that are statements
// rather than calls. Only this module's own code is walked, matching the call
// facts the evaluator sees: a goroutine a dependency launches is the
// dependency's business until something here calls into it.
func (i *Indexer) collectStmtEffects(fi *funcInfo) {
	if fi.body == nil || fi.pkg == nil || fi.pkg.TypesInfo == nil {
		return
	}
	info := fi.pkg.TypesInfo
	add := func(pos token.Pos, kind, access, callee string) {
		p := i.fset.Position(pos)
		i.directEffects[fi.id] = append(i.directEffects[fi.id], model.Effect{
			Kind: kind, Access: access, Callee: callee,
			File: p.Filename, Line: p.Line, Site: fi.id,
		})
	}
	ast.Inspect(fi.body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			// A closure's body is its own function's when it runs, and it is
			// not indexed as one — so its effects are attributed here, to the
			// function that wrote it. That's right for the common shapes
			// (`go func() {…}()`, a handler literal) and wrong for a closure
			// returned and never called, which is the rarer case to be wrong
			// about.
			return true
		case *ast.GoStmt:
			add(node.Pos(), "goroutine", "", "go "+exprName(node.Call.Fun))
		case *ast.SendStmt:
			add(node.Pos(), "channel", "write", exprName(node.Chan)+" <-")
		case *ast.UnaryExpr:
			if node.Op == token.ARROW {
				add(node.Pos(), "channel", "read", "<-"+exprName(node.X))
			}
		case *ast.RangeStmt:
			if t := info.TypeOf(node.X); t != nil {
				if _, ok := t.Underlying().(*types.Chan); ok {
					add(node.Pos(), "channel", "read", "range "+exprName(node.X))
				}
			}
		case *ast.AssignStmt:
			// `:=` declares locals and can't name a package-level variable;
			// every other token assigns to something that already exists.
			if node.Tok == token.DEFINE {
				return true
			}
			for _, lhs := range node.Lhs {
				if v := packageVar(info, lhs); v != nil {
					add(lhs.Pos(), "global", "write", qualifiedVar(v))
				}
			}
		case *ast.IncDecStmt:
			if v := packageVar(info, node.X); v != nil {
				add(node.Pos(), "global", "write", qualifiedVar(v))
			}
		}
		return true
	})
}

// packageVar resolves the variable an assignment target ultimately writes
// into, if it is package-level. The target is unwrapped structurally —
// `cfg.Debug`, `cache[k]`, `*ptr`, `pkg.Var` — down to the root identifier,
// because writing a field of a global or an element of a global map is a
// write to the global as far as a reader is concerned.
//
// Nil for a local, a parameter, a field of a local, or anything reached
// through a pointer whose origin this can't see: a local pointer may well
// alias a global, but saying so would be a guess.
func packageVar(info *types.Info, e ast.Expr) *types.Var {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.SelectorExpr:
			// `pkg.Var` is an identifier as far as the checker is
			// concerned; `global.Field` is a selection whose root is the
			// variable.
			if id, ok := x.X.(*ast.Ident); ok {
				if _, isPkg := info.Uses[id].(*types.PkgName); isPkg {
					return asPackageVar(info.Uses[x.Sel])
				}
			}
			e = x.X
		case *ast.Ident:
			obj := info.Uses[x]
			if obj == nil {
				obj = info.Defs[x]
			}
			return asPackageVar(obj)
		default:
			return nil
		}
	}
}

// asPackageVar is obj as a package-level variable, or nil. A variable is
// package-level when its scope is its package's; a local's is a function's,
// and a field's is nil.
func asPackageVar(obj types.Object) *types.Var {
	v, ok := obj.(*types.Var)
	if !ok || v.IsField() || v.Pkg() == nil || v.Parent() == nil {
		return nil
	}
	if v.Parent() != v.Pkg().Scope() {
		return nil
	}
	return v
}

// qualifiedVar names a package-level variable the way the source does:
// "config.Debug", by package name rather than import path, because that is
// the identifier a reader will search for.
func qualifiedVar(v *types.Var) string {
	return v.Pkg().Name() + "." + v.Name()
}

// exprName renders an expression short enough to label a statement with:
// "worker", "s.queue", "func()". It is a label, not source — the line is
// right there for anyone who wants the whole thing.
func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprName(x.X) + "." + x.Sel.Name
	case *ast.CallExpr:
		return exprName(x.Fun) + "()"
	case *ast.FuncLit:
		return "func()"
	case *ast.ParenExpr:
		return exprName(x.X)
	case *ast.IndexExpr:
		return exprName(x.X) + "[…]"
	case *ast.StarExpr:
		return "*" + exprName(x.X)
	case *ast.UnaryExpr:
		return x.Op.String() + exprName(x.X)
	default:
		return "…"
	}
}

// addBindingEffects folds the recognized outbound edges into the effects, as
// network. A gRPC call through a generated stub is recognized by the call-graph
// pass rather than by any call shape a rule could name, and it is exactly the
// kind of thing a reader wants a badge for. Sites a rule already marked as
// network on the same line are left alone, so http.Get isn't reported twice.
func (i *Indexer) addBindingEffects() {
	seen := map[string]bool{}
	for _, effs := range i.directEffects {
		for _, e := range effs {
			if e.Kind == "net" {
				seen[e.File+":"+itoa(e.Line)] = true
			}
		}
	}
	for _, b := range i.bindings {
		if b.Role != model.RoleOutbound || b.File == "" || b.Site == "" {
			continue
		}
		at := b.File + ":" + itoa(b.Line)
		if seen[at] {
			continue
		}
		seen[at] = true
		callee := b.Detail
		if callee == "" {
			callee = b.Kind
		}
		i.directEffects[b.Site] = append(i.directEffects[b.Site], model.Effect{
			Kind: "net", Callee: callee, Key: b.Key,
			File: b.File, Line: b.Line, Rule: b.Rule, Site: b.Site,
		})
	}
}

func itoa(n int) string { return fmt.Sprint(n) }

// Effects reports what a function does outside its own stack: directly, and
// through the callees the index can follow, up to effectDepth calls away and
// effectBudget functions in all. Cached for the life of the index.
func (i *Indexer) Effects(id TargetID) (*model.EffectReport, error) {
	i.effectsMu.Lock()
	rep, ok := i.effectsCache[id]
	i.effectsMu.Unlock()
	if !ok {
		i.mu.RLock()
		fi, known := i.funcs[id]
		if !known {
			i.mu.RUnlock()
			return nil, fmt.Errorf("unknown target %q", id)
		}
		rep = i.computeEffects(fi)
		i.mu.RUnlock()
		i.effectsMu.Lock()
		i.effectsCache[id] = rep
		i.effectsMu.Unlock()
	}
	// A copy, because the workspace qualifies ids in place before serving.
	out := *rep
	out.Effects = append([]model.Effect(nil), rep.Effects...)
	return &out, nil
}

// hasEffects is the cheap question the folded-call dot asks: does the callee
// do anything at all, directly or inherited. Same cache as Effects.
func (i *Indexer) hasEffects(id TargetID) bool {
	rep, err := i.Effects(id)
	return err == nil && len(rep.Effects) > 0
}

// computeEffects walks the call graph breadth-first from fi, so every reached
// function is reported at the shortest distance it can be reached at and
// through the first of fi's own callees that gets there. Caller holds i.mu.
func (i *Indexer) computeEffects(root *funcInfo) *model.EffectReport {
	type visit struct {
		fi    *funcInfo
		depth int
		// via is root's own call site this function was reached through, and
		// viaTarget the callee that call resolves to: what the evidence row
		// names as the hop, and what a click on it opens.
		via       *callInfo
		viaTarget TargetID
	}
	rep := &model.EffectReport{ID: root.id, Effects: []model.Effect{}}
	seen := map[TargetID]bool{root.id: true}
	queue := []visit{{fi: root}}
	visited := 0

walk:
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		visited++
		for _, e := range i.directEffects[v.fi.id] {
			e.Depth = v.depth
			e.SiteTitle = v.fi.title
			if v.via != nil {
				e.Via = v.viaTarget
				e.ViaCall = v.via.id
				if t, ok := i.funcs[v.viaTarget]; ok {
					e.ViaTitle = t.title
				}
			}
			rep.Effects = append(rep.Effects, e)
		}
		for _, c := range v.fi.calls {
			// A reference names a function without running it, and a fan-out
			// has receivers rather than a callee; neither is a hop execution
			// takes from here.
			var targets []TargetID
			switch c.kind {
			case KindDirect:
				targets = []TargetID{c.target}
			case KindInterface:
				for _, cand := range c.candidates {
					targets = append(targets, cand.TargetID)
				}
			}
			for _, t := range targets {
				if t == "" || seen[t] {
					continue
				}
				seen[t] = true
				tf, ok := i.funcs[t]
				// Dependency bodies carry no effects of their own — the
				// evaluator only sees this module's call sites — so walking
				// them would spend the budget to learn nothing.
				if !ok || tf.body == nil || !i.ownsCode(tf) {
					continue
				}
				if v.depth >= effectDepth || visited+len(queue) >= effectBudget {
					rep.Truncated = true
					if visited+len(queue) >= effectBudget {
						break walk
					}
					continue
				}
				via, viaTarget := v.via, v.viaTarget
				if v.depth == 0 {
					via, viaTarget = c, t
				}
				queue = append(queue, visit{fi: tf, depth: v.depth + 1, via: via, viaTarget: viaTarget})
			}
		}
	}

	// Direct evidence first, then by kind, then in source order — the order
	// the popover lists them in, and stable across rebuilds.
	sort.SliceStable(rep.Effects, func(a, b int) bool {
		x, y := rep.Effects[a], rep.Effects[b]
		if x.Depth != y.Depth {
			return x.Depth < y.Depth
		}
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		if x.File != y.File {
			return x.File < y.File
		}
		return x.Line < y.Line
	})
	return rep
}
