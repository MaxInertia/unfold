// Package indexer loads Go packages and builds the call-site index that
// powers unfold's interactive code expansion.
//
// Phase 1: direct calls only. Calls that go through an interface are
// recorded with kind="interface" but their candidate implementations are
// not enumerated yet — that arrives in Phase 2. Calls through a function
// value (e.g. f := obj.Foo; f()) are recorded as kind="indirect".
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
	// interface (until Phase 2) and indirect calls.
	TargetID TargetID `json:"targetId,omitempty"`
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
	// (for direct calls) target.
	callsByID map[CallID]*callInfo

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
	target      TargetID // direct target (empty otherwise)
	displayName string
	pos, end    token.Pos
}

// New returns a fresh, empty indexer.
func New() *Indexer {
	return &Indexer{
		funcs:     make(map[TargetID]*funcInfo),
		callsByID: make(map[CallID]*callInfo),
		fileBytes: make(map[string][]byte),
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
	pos := i.fset.Position(ce.Pos())
	id := CallID(fmt.Sprintf("%s:%d", pos.Filename, pos.Offset))

	ci := &callInfo{
		id:     id,
		parent: parent.id,
		pos:    ce.Pos(),
		end:    ce.End(),
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
				// Phase 1: don't enumerate candidates.
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

func formatSelector(s *ast.SelectorExpr) string {
	switch x := s.X.(type) {
	case *ast.Ident:
		return x.Name + "." + s.Sel.Name
	default:
		return s.Sel.Name
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

// FrameForCall returns a Frame for the resolved target of the given call.
// For direct calls this is unambiguous; for interface/indirect calls it
// returns an error in Phase 1.
func (i *Indexer) FrameForCall(id CallID) (*Frame, error) {
	i.mu.RLock()
	c, ok := i.callsByID[id]
	i.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown call %q", id)
	}
	if c.kind != KindDirect {
		return nil, fmt.Errorf("call %q is %s; expansion not supported in Phase 1", id, c.kind)
	}
	return i.Frame(c.target)
}

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
