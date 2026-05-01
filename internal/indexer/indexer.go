// Package indexer loads Go packages and builds the call-site and
// implementer indexes that power unfold's interactive code expansion.
//
// Phase 2: direct and interface calls are resolved. Interface calls
// carry a list of concrete-type implementations; the API picks one as
// the default expansion target and lets the caller switch via choice.
// Calls through a function value or a builtin (len, make, ...) are
// recorded as kind="indirect" and remain unexpandable.
package indexer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// TargetID uniquely identifies a function across the loaded module set.
// It is the qualified name from go/types (*types.Func).FullName, e.g.
// "github.com/x/y.(*T).Method" or "github.com/x/y.Func".
type TargetID string

// CallID uniquely identifies a call site. It is "<file>:<byte-offset>".
type CallID string

// CallKind classifies a call site by how its target is resolved.
type CallKind string

const (
	KindDirect    CallKind = "direct"    // resolved to one specific function
	KindInterface CallKind = "interface" // dispatched through an interface (Phase 2 enumerates candidates)
	KindIndirect  CallKind = "indirect"  // through a function value, builtin, or otherwise unresolvable
)

// Frame is the unit the frontend renders: a function's source plus the
// call sites inside it. Byte offsets in CallSite.SpanStart/SpanEnd are
// relative to Source (not to the original file) so the frontend can
// decorate spans without knowing where the body lives on disk.
type Frame struct {
	ID        TargetID   `json:"id"`
	File      string     `json:"file"`
	Language  string     `json:"language"`
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
	Source    string     `json:"source"`
	Calls     []CallSite `json:"calls"`
}

// CallSite describes one call inside a function body.
type CallSite struct {
	ID          CallID   `json:"id"`
	SpanStart   int      `json:"spanStart"`
	SpanEnd     int      `json:"spanEnd"`
	DisplayName string   `json:"displayName"`
	Kind        CallKind `json:"kind"`

	// TargetID is set for direct calls (the resolved target). Empty for
	// interface and indirect calls.
	TargetID TargetID `json:"targetId,omitempty"`

	// Candidates lists possible expansion targets for interface calls.
	// Empty for direct (TargetID is the only target) and indirect calls.
	// The first candidate is the default chosen by /api/body when no
	// choice query param is supplied.
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Candidate is one concrete implementation of an interface method, used
// to populate the impl-switcher dropdown.
type Candidate struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"` // e.g. "*foo.RealService.Process"
}

// SearchResult is one hit returned from Indexer.Search.
type SearchResult struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
}

// Indexer holds loaded packages and the per-function call-site index.
type Indexer struct {
	mu sync.RWMutex

	pkgs []*packages.Package
	fset *token.FileSet

	// funcs is keyed by TargetID (i.e. *types.Func.FullName()).
	funcs map[TargetID]*funcInfo

	// callsByID lets the server resolve a CallID to its parent function and
	// pick a candidate target for interface calls.
	callsByID map[CallID]*callInfo

	// interfaceImpls maps a named-interface key (pkgpath.TypeName) to
	// the list of concrete types in the loaded set that implement it.
	// Built once during Load.
	interfaceImpls map[string][]types.Type

	// fileBytes caches the raw source of files whose functions we've
	// produced frames for, so we don't re-read on every /body request.
	fileBytesMu sync.Mutex
	fileBytes   map[string][]byte
}

type funcInfo struct {
	id    TargetID
	obj   *types.Func
	decl  *ast.FuncDecl
	pkg   *packages.Package
	calls []*callInfo
}

type callInfo struct {
	id          CallID
	parent      TargetID
	kind        CallKind
	target      TargetID    // direct target (empty otherwise)
	candidates  []Candidate // interface candidates (in stable order)
	displayName string
	pos, end    token.Pos
}

// New returns a fresh, empty indexer.
func New() *Indexer {
	return &Indexer{
		funcs:          make(map[TargetID]*funcInfo),
		callsByID:      make(map[CallID]*callInfo),
		interfaceImpls: make(map[string][]types.Type),
		fileBytes:      make(map[string][]byte),
	}
}

// Load parses and type-checks the Go packages matched by pattern (e.g.
// "./...") relative to dir, and builds the call-site index. If dir is
// empty, the current working directory is used. Packages with type
// errors are kept and indexed best-effort; their errors are written to
// stderr.
func (i *Indexer) Load(dir, pattern string) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedTypesSizes |
			packages.NeedModule,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return fmt.Errorf("packages.Load: %w", err)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages matched %q", pattern)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		fmt.Fprintf(os.Stderr, "unfold: %d package errors (continuing)\n", n)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	i.pkgs = pkgs
	i.fset = pkgs[0].Fset
	i.funcs = make(map[TargetID]*funcInfo)
	i.callsByID = make(map[CallID]*callInfo)
	i.interfaceImpls = buildInterfaceImpls(pkgs)

	// Pass 1: register every FuncDecl as a target.
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.TypesInfo == nil {
			return
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}
				obj, _ := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
				if obj == nil {
					return true
				}
				tid := TargetID(obj.FullName())
				if _, dup := i.funcs[tid]; dup {
					// Two FuncDecls with the same FullName shouldn't happen
					// inside one type-checked package set; if it does (e.g.
					// build constraints), keep the first.
					return true
				}
				i.funcs[tid] = &funcInfo{id: tid, obj: obj, decl: fd, pkg: pkg}
				return true
			})
		}
	})

	// Pass 2: walk each function body, resolve call sites.
	for _, fi := range i.funcs {
		if fi.decl.Body == nil {
			continue
		}
		ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ci := i.resolveCall(fi, ce)
			if ci == nil {
				return true
			}
			fi.calls = append(fi.calls, ci)
			i.callsByID[ci.id] = ci
			return true
		})
	}

	return nil
}

func (i *Indexer) resolveCall(parent *funcInfo, ce *ast.CallExpr) *callInfo {
	// Span only the function-name token, not the whole call expression.
	// This avoids overlapping decorations for nested calls (Shiki rejects
	// overlap). For example fmt.Sprintf("...", listener.Addr().String())
	// yields three disjoint spans on "Sprintf", "Addr", and "String".
	//
	// We skip immediately-invoked function literals — there's no name to
	// click on, and their body is inline anyway.
	spanPos, spanEnd, ok := nameSpan(ce.Fun)
	if !ok {
		return nil
	}
	pos := i.fset.Position(ce.Pos())
	id := CallID(fmt.Sprintf("%s:%d", pos.Filename, pos.Offset))

	ci := &callInfo{
		id:     id,
		parent: parent.id,
		pos:    spanPos,
		end:    spanEnd,
		kind:   KindIndirect, // overwritten below if resolvable
	}

	info := parent.pkg.TypesInfo
	if info == nil {
		return ci
	}

	switch fn := ce.Fun.(type) {
	case *ast.Ident:
		// foo()  — package-level function or local name
		if obj, ok := info.Uses[fn].(*types.Func); ok {
			ci.kind = KindDirect
			ci.target = TargetID(obj.FullName())
			ci.displayName = fn.Name
		} else {
			ci.displayName = fn.Name
		}

	case *ast.SelectorExpr:
		// x.Foo() — method call, package selector, or field-of-func
		ci.displayName = formatSelector(fn)
		if sel, ok := info.Selections[fn]; ok {
			// Real method/field selection.
			fnObj, _ := sel.Obj().(*types.Func)
			if fnObj == nil {
				return ci
			}
			recv := sel.Recv()
			if isInterface(recv) {
				ci.kind = KindInterface
				ci.candidates = i.candidatesFor(recv, fnObj.Name())
				return ci
			}
			ci.kind = KindDirect
			ci.target = TargetID(fnObj.FullName())
			return ci
		}
		// Package-qualified call: pkg.Func() — info.Uses[fn.Sel].
		if obj, ok := info.Uses[fn.Sel].(*types.Func); ok {
			ci.kind = KindDirect
			ci.target = TargetID(obj.FullName())
		}

	default:
		// Function literals invoked immediately, type conversions, etc.
		// Leave as indirect.
	}

	return ci
}

func isInterface(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// buildInterfaceImpls walks every named type in the loaded package set
// and, for every named interface, records the concrete types (or pointer
// types) that satisfy it. Anonymous interfaces and the empty interface
// are skipped — anonymous because they have no stable lookup key, empty
// because every type would qualify.
func buildInterfaceImpls(pkgs []*packages.Package) map[string][]types.Type {
	var (
		concretes []*types.Named
		ifaces    []*types.Named
	)
	seen := make(map[*types.Named]bool)
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.Types == nil {
			return
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			n, ok := obj.Type().(*types.Named)
			if !ok || seen[n] {
				continue
			}
			seen[n] = true
			if _, isIface := n.Underlying().(*types.Interface); isIface {
				ifaces = append(ifaces, n)
			} else {
				concretes = append(concretes, n)
			}
		}
	})

	impls := make(map[string][]types.Type)
	for _, iface := range ifaces {
		ifaceT, _ := iface.Underlying().(*types.Interface)
		if ifaceT == nil || ifaceT.NumMethods() == 0 {
			continue
		}
		key := interfaceKey(iface)
		if key == "" {
			continue
		}
		for _, n := range concretes {
			switch {
			case types.Implements(n, ifaceT):
				impls[key] = append(impls[key], n)
			case types.Implements(types.NewPointer(n), ifaceT):
				impls[key] = append(impls[key], types.NewPointer(n))
			}
		}
		// Stable order so candidate indexes are deterministic.
		sort.Slice(impls[key], func(a, b int) bool {
			return types.TypeString(impls[key][a], nil) < types.TypeString(impls[key][b], nil)
		})
	}
	return impls
}

// interfaceKey returns a stable string key for a named interface type.
func interfaceKey(n *types.Named) string {
	obj := n.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// candidatesFor enumerates concrete-method candidates for a call through
// the given interface receiver and method name.
func (i *Indexer) candidatesFor(recv types.Type, methodName string) []Candidate {
	named, ok := recv.(*types.Named)
	if !ok {
		// Anonymous interface — leave candidates empty; the frontend will
		// render a non-expandable interface call.
		return nil
	}
	key := interfaceKey(named)
	if key == "" {
		return nil
	}
	implTypes := i.interfaceImpls[key]
	if len(implTypes) == 0 {
		return nil
	}

	out := make([]Candidate, 0, len(implTypes))
	for _, t := range implTypes {
		ms := types.NewMethodSet(t)
		for j := 0; j < ms.Len(); j++ {
			fn, _ := ms.At(j).Obj().(*types.Func)
			if fn == nil || fn.Name() != methodName {
				continue
			}
			// Only surface methods we actually have FuncDecl source for —
			// i.e. they're in the loaded package set.
			id := TargetID(fn.FullName())
			if _, known := i.funcs[id]; !known {
				continue
			}
			out = append(out, Candidate{
				TargetID: id,
				Label:    types.TypeString(t, types.RelativeTo(named.Obj().Pkg())) + "." + fn.Name(),
			})
			break
		}
	}
	return out
}

func formatSelector(s *ast.SelectorExpr) string {
	switch x := s.X.(type) {
	case *ast.Ident:
		return x.Name + "." + s.Sel.Name
	default:
		return s.Sel.Name
	}
}

// nameSpan returns the byte range of the function-name token in a call's
// Fun expression. For x.Method() it's the range of "Method"; for plain
// identifiers and qualified names it's the identifier itself. Returns
// false for function literals invoked immediately, which have no name
// to click on.
func nameSpan(fun ast.Expr) (token.Pos, token.Pos, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Pos(), f.End(), true
	case *ast.SelectorExpr:
		return f.Sel.Pos(), f.Sel.End(), true
	case *ast.IndexExpr:
		// Generic instantiation: Foo[T](args). Span the indexed name.
		return nameSpan(f.X)
	case *ast.IndexListExpr:
		return nameSpan(f.X)
	case *ast.ParenExpr:
		return nameSpan(f.X)
	case *ast.FuncLit:
		// IIFE — no name token, skip.
		return 0, 0, false
	default:
		// Type conversions, less common forms — fall back to the full Fun.
		// These rarely nest inside other calls in a way that overlaps.
		return f.Pos(), f.End(), true
	}
}

// Frame returns a Frame for the given target. Returns an error if the
// target is unknown or its body source can't be read.
func (i *Indexer) Frame(id TargetID) (*Frame, error) {
	i.mu.RLock()
	fi, ok := i.funcs[id]
	i.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown target %q", id)
	}
	if fi.decl.Body == nil {
		return nil, fmt.Errorf("target %q has no body", id)
	}

	startPos := i.fset.Position(fi.decl.Pos())
	endPos := i.fset.Position(fi.decl.End())
	src, err := i.readRange(startPos.Filename, startPos.Offset, endPos.Offset)
	if err != nil {
		return nil, err
	}

	calls := make([]CallSite, 0, len(fi.calls))
	base := startPos.Offset
	for _, c := range fi.calls {
		callStart := i.fset.Position(c.pos).Offset - base
		callEnd := i.fset.Position(c.end).Offset - base
		calls = append(calls, CallSite{
			ID:          c.id,
			SpanStart:   callStart,
			SpanEnd:     callEnd,
			DisplayName: c.displayName,
			Kind:        c.kind,
			TargetID:    c.target,
			Candidates:  c.candidates,
		})
	}

	return &Frame{
		ID:        id,
		File:      startPos.Filename,
		Language:  "go",
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		Source:    string(src),
		Calls:     calls,
	}, nil
}

// FrameForCall returns a Frame for the chosen target of the given call.
//
// For direct calls, choice is ignored.
// For interface calls, choice indexes into the call's Candidates list
// (clamped to a valid range; choice<0 or out-of-range becomes 0).
// Indirect calls (function values, builtins like make/len) are not
// expandable — FrameForCall returns an error in that case.
//
// Returns ErrNoCandidates if an interface call has zero known candidates.
func (i *Indexer) FrameForCall(id CallID, choice int) (*Frame, error) {
	i.mu.RLock()
	c, ok := i.callsByID[id]
	i.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown call %q", id)
	}
	switch c.kind {
	case KindDirect:
		return i.Frame(c.target)
	case KindInterface:
		if len(c.candidates) == 0 {
			return nil, ErrNoCandidates
		}
		if choice < 0 || choice >= len(c.candidates) {
			choice = 0
		}
		return i.Frame(c.candidates[choice].TargetID)
	default:
		return nil, fmt.Errorf("call %q is %s; not expandable", id, c.kind)
	}
}

// ErrNoCandidates is returned by FrameForCall when an interface call has
// no known concrete implementations in the loaded package set.
var ErrNoCandidates = fmt.Errorf("no candidate implementations found for interface call")

// LookupSymbol resolves a symbol name (qualified or unqualified) to a
// target. If multiple match, the first lexicographic FullName wins.
func (i *Indexer) LookupSymbol(name string) (TargetID, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if name == "" {
		return "", fmt.Errorf("empty symbol")
	}

	// Exact full-name match wins.
	if _, ok := i.funcs[TargetID(name)]; ok {
		return TargetID(name), nil
	}

	// Otherwise: case-sensitive suffix match on the basename.
	var candidates []TargetID
	for id := range i.funcs {
		if matchesSymbol(string(id), name) {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no symbol matches %q", name)
	}
	sort.Slice(candidates, func(a, b int) bool {
		return string(candidates[a]) < string(candidates[b])
	})
	return candidates[0], nil
}

func matchesSymbol(full, query string) bool {
	// Match query against the basename: everything after the last '.' for
	// package functions, after the last ".(*T)." or ".(T)." for methods.
	dot := strings.LastIndex(full, ".")
	if dot < 0 {
		return full == query
	}
	return full[dot+1:] == query || full == query
}

// Search returns up to `limit` symbols whose FullName contains query
// (case-insensitive). A simple substring search; the frontend can do its
// own ranking later.
func (i *Indexer) Search(query string, limit int) []SearchResult {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(query)

	out := make([]SearchResult, 0, limit)
	ids := make([]TargetID, 0, len(i.funcs))
	for id := range i.funcs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return string(ids[a]) < string(ids[b]) })

	for _, id := range ids {
		if q != "" && !strings.Contains(strings.ToLower(string(id)), q) {
			continue
		}
		fi := i.funcs[id]
		pos := i.fset.Position(fi.decl.Pos())
		out = append(out, SearchResult{
			TargetID: id,
			Label:    string(id),
			File:     pos.Filename,
			Line:     pos.Line,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// readRange reads bytes [start, end) from filename, caching file contents.
func (i *Indexer) readRange(filename string, start, end int) ([]byte, error) {
	i.fileBytesMu.Lock()
	defer i.fileBytesMu.Unlock()

	buf, ok := i.fileBytes[filename]
	if !ok {
		b, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filename, err)
		}
		i.fileBytes[filename] = b
		buf = b
	}
	if start < 0 || end > len(buf) || start > end {
		return nil, fmt.Errorf("range [%d,%d) out of bounds for %s (len %d)", start, end, filename, len(buf))
	}
	return buf[start:end], nil
}
