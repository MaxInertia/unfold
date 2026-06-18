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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MaxInertia/unfold/internal/model"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// utf16Offset returns the number of UTF-16 code units in b[:byteOffset].
// The frontend indexes a function's source as a JavaScript (UTF-16) string,
// so call-site span offsets must be expressed in UTF-16 units, not UTF-8
// bytes. ASCII text counts one unit per byte (this is the identity); runes
// above the BMP count as two units (a surrogate pair).
func utf16Offset(b []byte, byteOffset int) int {
	if byteOffset > len(b) {
		byteOffset = len(b)
	}
	n := 0
	for i := 0; i < byteOffset; {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			n++ // invalid byte — count it as one unit and advance
			i++
			continue
		}
		if r > 0xFFFF {
			n += 2 // encoded as a surrogate pair in UTF-16
		} else {
			n++
		}
		i += size
	}
	return n
}

// byteOffsetForUTF16 is the inverse of utf16Offset: it maps a UTF-16 code-unit
// offset (what the frontend sends) back to a byte offset in b.
func byteOffsetForUTF16(b []byte, u16 int) int {
	n := 0
	for i := 0; i < len(b); {
		if n >= u16 {
			return i
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			n++
			i++
			continue
		}
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return len(b)
}

// The wire types live in internal/model so every engine emits the same
// JSON shapes. These aliases keep the indexer's call sites terse and let
// existing callers/tests continue to reference indexer.Frame etc. For the
// Go engine, TargetID is *types.Func.FullName (e.g.
// "github.com/x/y.(*T).Method") and CallID is "<file>:<byte-offset>".
type (
	TargetID     = model.TargetID
	CallID       = model.CallID
	CallKind     = model.CallKind
	Frame        = model.Frame
	CallSite     = model.CallSite
	Candidate    = model.Candidate
	SearchResult = model.SearchResult
	TypeInfo     = model.TypeInfo
)

const (
	KindDirect    = model.KindDirect
	KindInterface = model.KindInterface
	KindIndirect  = model.KindIndirect
)

// Indexer implements model.Engine.
var _ model.Engine = (*Indexer)(nil)

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

	// usagesByTarget is the reverse of the call-site index: for each target,
	// the places it's referenced (direct calls, interface calls that may
	// dispatch to it, and value references). Built once during Load.
	usagesByTarget map[TargetID][]*usageInfo

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
	goroutine   bool // call is launched with the `go` keyword
}

// usageInfo is one reverse-index entry: a place a target is referenced.
type usageInfo struct {
	call   *callInfo // the call site; nil for value references
	choice int       // candidate index selecting the target at that call
	parent TargetID  // enclosing function
	kind   model.UsageKind
	pos    token.Pos // the usage's name token
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
	i.usagesByTarget = make(map[TargetID][]*usageInfo)

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

	// Pass 2: walk each function body, resolve call sites. Idents that name
	// an indexed function but are not a call's name token are recorded as
	// value references (the function passed around as a value).
	for _, fi := range i.funcs {
		if fi.decl.Body == nil {
			continue
		}
		// goLaunched collects the CallExpr that are the operand of a `go`
		// statement. ast.Inspect visits a node before its children, so a
		// GoStmt is always seen before its own Call — the set is populated
		// by the time we resolve that CallExpr below. callNames works the
		// same way: a CallExpr is visited before the Ident that names it.
		goLaunched := make(map[*ast.CallExpr]bool)
		callNames := make(map[*ast.Ident]bool)
		ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				goLaunched[node.Call] = true
			case *ast.CallExpr:
				if name := nameIdent(node.Fun); name != nil {
					callNames[name] = true
				}
				ci := i.resolveCall(fi, node)
				if ci == nil {
					return true
				}
				if goLaunched[node] {
					ci.goroutine = true
				}
				fi.calls = append(fi.calls, ci)
				i.callsByID[ci.id] = ci
			case *ast.Ident:
				if callNames[node] {
					return true
				}
				obj, ok := fi.pkg.TypesInfo.Uses[node].(*types.Func)
				if !ok {
					return true
				}
				tid := TargetID(obj.FullName())
				if _, known := i.funcs[tid]; known {
					i.usagesByTarget[tid] = append(i.usagesByTarget[tid], &usageInfo{
						parent: fi.id,
						kind:   model.UsageRef,
						pos:    node.Pos(),
					})
				}
			}
			return true
		})
	}

	// Reverse index over call sites: a direct call references its target; an
	// interface call references every candidate it may dispatch to (Choice
	// records the candidate's index so the frontend can reproduce this usage
	// as an expansion via FrameForCall).
	for _, c := range i.callsByID {
		switch c.kind {
		case KindDirect:
			if _, known := i.funcs[c.target]; known {
				i.usagesByTarget[c.target] = append(i.usagesByTarget[c.target], &usageInfo{
					call:   c,
					parent: c.parent,
					kind:   model.UsageCall,
					pos:    c.pos,
				})
			}
		case KindInterface:
			for j, cand := range c.candidates {
				i.usagesByTarget[cand.TargetID] = append(i.usagesByTarget[cand.TargetID], &usageInfo{
					call:   c,
					choice: j,
					parent: c.parent,
					kind:   model.UsageInterface,
					pos:    c.pos,
				})
			}
		}
	}

	return nil
}

// nameIdent returns the identifier that names a call's function — the same
// token nameSpan spans — or nil when there is none (IIFE, conversions).
func nameIdent(fun ast.Expr) *ast.Ident {
	switch f := fun.(type) {
	case *ast.Ident:
		return f
	case *ast.SelectorExpr:
		return f.Sel
	case *ast.IndexExpr:
		return nameIdent(f.X)
	case *ast.IndexListExpr:
		return nameIdent(f.X)
	case *ast.ParenExpr:
		return nameIdent(f.X)
	default:
		return nil
	}
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
	if path, ok := strings.CutPrefix(string(id), "file:"); ok {
		return i.fileFrame(path)
	}
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

	// Span offsets are reported as UTF-16 code-unit indices into Source,
	// because the frontend indexes the source as a JavaScript (UTF-16)
	// string. Emitting raw UTF-8 byte offsets would drift the highlight
	// right by one unit per extra byte of any non-ASCII rune before the
	// span (e.g. an em-dash in a comment is 3 bytes but 1 UTF-16 unit).
	calls := make([]CallSite, 0, len(fi.calls))
	base := startPos.Offset
	for _, c := range fi.calls {
		byteStart := i.fset.Position(c.pos).Offset - base
		byteEnd := i.fset.Position(c.end).Offset - base
		calls = append(calls, CallSite{
			ID:          c.id,
			SpanStart:   utf16Offset(src, byteStart),
			SpanEnd:     utf16Offset(src, byteEnd),
			DisplayName: c.displayName,
			Kind:        c.kind,
			TargetID:    c.target,
			Candidates:  c.candidates,
			Goroutine:   c.goroutine,
			External:    c.kind == KindDirect && i.isExternal(c.target),
		})
	}

	return &Frame{
		ID:        id,
		Title:     goTitle(fi.obj),
		File:      startPos.Filename,
		Language:  "go",
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		Source:    string(src),
		Calls:     calls,
	}, nil
}

// goTitle returns a short, display-friendly name for a function: "Func" for
// package functions, "Recv.Method" for methods. (The TargetID is the fully
// qualified FullName, which is too long for a header or bookmark label.)
func goTitle(obj *types.Func) string {
	name := obj.Name()
	sig, _ := obj.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return name
	}
	recv := sig.Recv().Type()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	if named, ok := recv.(*types.Named); ok {
		return named.Obj().Name() + "." + name
	}
	return name
}

// Files returns the sorted, distinct absolute paths of files that hold at
// least one indexed function. Only files in the main module are listed —
// dependency and stdlib sources (also loaded for resolution) are excluded
// so the tree shows the project the user is reading, not its dep graph.
func (i *Indexer) Files() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	set := make(map[string]struct{}, len(i.funcs))
	for _, fi := range i.funcs {
		if fi.pkg == nil || fi.pkg.Module == nil || !fi.pkg.Module.Main {
			continue
		}
		set[i.fset.Position(fi.decl.Pos()).Filename] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// isExternal reports whether a direct target's package lives outside the
// main module (stdlib or a dependency). Such calls are still expandable,
// but the frontend's bulk "+1 level" skips them. (The TS engine never sets
// this: it only registers project files, so external calls already carry
// no target.)
func (i *Indexer) isExternal(id TargetID) bool {
	if id == "" {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	fi, ok := i.funcs[id]
	if !ok {
		return false
	}
	return fi.pkg == nil || fi.pkg.Module == nil || !fi.pkg.Module.Main
}

// fileFrame builds a Frame for a whole file: the full source plus every call
// site across all of the file's functions, with offsets relative to the file
// start. The call IDs match those produced during indexing, so expanding a
// call from the file view works through the normal FrameForCall path.
func (i *Indexer) fileFrame(path string) (*Frame, error) {
	buf, err := i.readFile(path)
	if err != nil {
		return nil, err
	}

	i.mu.RLock()
	var infos []*callInfo
	for _, fi := range i.funcs {
		if i.fset.Position(fi.decl.Pos()).Filename != path {
			continue
		}
		infos = append(infos, fi.calls...)
	}
	i.mu.RUnlock()

	sort.Slice(infos, func(a, b int) bool { return infos[a].pos < infos[b].pos })
	calls := make([]CallSite, 0, len(infos))
	for _, c := range infos {
		calls = append(calls, CallSite{
			ID:          c.id,
			SpanStart:   utf16Offset(buf, i.fset.Position(c.pos).Offset),
			SpanEnd:     utf16Offset(buf, i.fset.Position(c.end).Offset),
			DisplayName: c.displayName,
			Kind:        c.kind,
			TargetID:    c.target,
			Candidates:  c.candidates,
			Goroutine:   c.goroutine,
			External:    c.kind == KindDirect && i.isExternal(c.target),
		})
	}

	return &Frame{
		ID:        TargetID("file:" + path),
		Title:     filepath.Base(path),
		File:      path,
		Language:  "go",
		StartLine: 1,
		EndLine:   1 + strings.Count(string(buf), "\n"),
		Source:    string(buf),
		Calls:     calls,
	}, nil
}

// readFile returns the full bytes of path, caching like readRange.
func (i *Indexer) readFile(path string) ([]byte, error) {
	i.fileBytesMu.Lock()
	defer i.fileBytesMu.Unlock()
	if buf, ok := i.fileBytes[path]; ok {
		return buf, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	i.fileBytes[path] = b
	return b, nil
}

// TypeInfo resolves the identifier at a UTF-16 offset into the frame's source
// and reports its type details. Returns nil (no error) when the offset isn't
// over a resolvable identifier. A negative offset describes the target's own
// declaration — used by note references, which know a target but no hover
// position.
func (i *Indexer) TypeInfo(id TargetID, offset int) (*TypeInfo, error) {
	if offset < 0 {
		return i.describeTarget(id)
	}
	var (
		srcBase  int // byte offset in the file where the frame source starts
		fileName string
		astFile  *ast.File
		pkg      *packages.Package
	)
	if path, ok := strings.CutPrefix(string(id), "file:"); ok {
		astFile, pkg = i.astFileFor(path)
		fileName = path
	} else {
		i.mu.RLock()
		fi, ok := i.funcs[id]
		i.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown target %q", id)
		}
		start := i.fset.Position(fi.decl.Pos())
		srcBase, fileName, pkg = start.Offset, start.Filename, fi.pkg
		astFile = fileContaining(fi.decl, fi.pkg)
	}
	if astFile == nil || pkg == nil || pkg.TypesInfo == nil {
		return nil, nil
	}

	buf, err := i.readFile(fileName)
	if err != nil {
		return nil, err
	}
	if srcBase > len(buf) {
		return nil, nil
	}
	abs := srcBase + byteOffsetForUTF16(buf[srcBase:], offset)

	tf := i.fset.File(astFile.Pos())
	if tf == nil || abs < 0 || abs > tf.Size() {
		return nil, nil
	}
	pos := tf.Pos(abs)

	enclosing, _ := astutil.PathEnclosingInterval(astFile, pos, pos)
	var ident *ast.Ident
	for _, n := range enclosing {
		if id2, ok := n.(*ast.Ident); ok {
			ident = id2
			break
		}
	}
	if ident == nil {
		return nil, nil
	}
	obj := pkg.TypesInfo.ObjectOf(ident)
	if obj == nil {
		return nil, nil
	}

	ti := &TypeInfo{
		Name:       ident.Name,
		Kind:       objKind(obj),
		Type:       types.TypeString(obj.Type(), types.RelativeTo(pkg.Types)),
		Definition: typeDefinition(obj.Type(), types.RelativeTo(pkg.Types)),
	}
	if obj.Pos().IsValid() {
		dp := i.fset.Position(obj.Pos())
		ti.DefinedAt = fmt.Sprintf("%s:%d", dp.Filename, dp.Line)
	}
	if fn, ok := obj.(*types.Func); ok {
		// Only expose TargetID for functions we actually indexed — otherwise
		// the hover card would offer "open" on a stdlib/dependency func that
		// LookupSymbol can't resolve, yielding a dead link. (The TS engine
		// gates this the same way.)
		fullName := TargetID(fn.FullName())
		i.mu.RLock()
		if dfi, ok := i.funcs[fullName]; ok {
			ti.TargetID = fullName
			if dfi.decl.Doc != nil {
				ti.Doc = strings.TrimSpace(dfi.decl.Doc.Text())
			}
		}
		i.mu.RUnlock()
	}
	return ti, nil
}

// typeDefinition expands a type's shape when the name alone isn't telling:
// a named struct's fields, a named interface's methods, or a named alias's
// underlying type. Pointers are dereferenced first. Returns "" for types
// whose TypeString already says everything (basics, slices of basics,
// funcs, unnamed types).
func typeDefinition(t types.Type, qual types.Qualifier) string {
	for {
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
			continue
		}
		break
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	switch u := named.Underlying().(type) {
	case *types.Struct:
		if u.NumFields() == 0 {
			return "struct{}"
		}
		var b strings.Builder
		b.WriteString("struct {\n")
		for f := 0; f < u.NumFields(); f++ {
			field := u.Field(f)
			b.WriteString("    ")
			if !field.Embedded() {
				b.WriteString(field.Name())
				b.WriteString(" ")
			}
			b.WriteString(types.TypeString(field.Type(), qual))
			b.WriteString("\n")
		}
		b.WriteString("}")
		return b.String()
	case *types.Interface:
		if u.NumMethods() == 0 {
			return "interface{}"
		}
		var b strings.Builder
		b.WriteString("interface {\n")
		for m := 0; m < u.NumMethods(); m++ {
			fn := u.Method(m)
			sig := types.TypeString(fn.Type(), qual)
			b.WriteString("    ")
			b.WriteString(fn.Name())
			b.WriteString(strings.TrimPrefix(sig, "func"))
			b.WriteString("\n")
		}
		b.WriteString("}")
		return b.String()
	case *types.Signature:
		return "" // the Type field already shows the signature
	default:
		// Named alias of a basic/slice/map/chan: show what it really is.
		def := types.TypeString(u, qual)
		if def == types.TypeString(named, qual) {
			return ""
		}
		return def
	}
}

// describeTarget builds the TypeInfo of a target's own declaration.
func (i *Indexer) describeTarget(id TargetID) (*TypeInfo, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	fi, ok := i.funcs[id]
	if !ok {
		return nil, nil
	}
	ti := &TypeInfo{
		Kind:     "func",
		Name:     goTitle(fi.obj),
		Type:     types.TypeString(fi.obj.Type(), types.RelativeTo(fi.pkg.Types)),
		TargetID: id,
	}
	dp := i.fset.Position(fi.decl.Pos())
	ti.DefinedAt = fmt.Sprintf("%s:%d", dp.Filename, dp.Line)
	if fi.decl.Doc != nil {
		ti.Doc = strings.TrimSpace(fi.decl.Doc.Text())
	}
	return ti, nil
}

// astFileFor finds the parsed file and its package for a given path.
func (i *Indexer) astFileFor(path string) (*ast.File, *packages.Package) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, pkg := range i.pkgs {
		for _, f := range pkg.Syntax {
			if i.fset.Position(f.Pos()).Filename == path {
				return f, pkg
			}
		}
	}
	return nil, nil
}

func fileContaining(node ast.Node, pkg *packages.Package) *ast.File {
	for _, f := range pkg.Syntax {
		if f.Pos() <= node.Pos() && node.Pos() < f.End() {
			return f
		}
	}
	return nil
}

func objKind(obj types.Object) string {
	switch o := obj.(type) {
	case *types.Var:
		if o.IsField() {
			return "field"
		}
		return "var"
	case *types.Func:
		return "func"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.PkgName:
		return "package"
	case *types.Label:
		return "label"
	case *types.Builtin:
		return "builtin"
	default:
		return "symbol"
	}
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

// Usages returns the places the target is referenced inside indexed
// function bodies, sorted by file then line. Excerpts are a few lines of
// context around the usage, clamped to the enclosing function's body.
func (i *Indexer) Usages(id TargetID) ([]model.Usage, error) {
	i.mu.RLock()
	if _, ok := i.funcs[id]; !ok {
		i.mu.RUnlock()
		return nil, fmt.Errorf("unknown target %q", id)
	}
	infos := i.usagesByTarget[id]
	out := make([]model.Usage, 0, len(infos))
	for _, u := range infos {
		parent, ok := i.funcs[u.parent]
		if !ok {
			continue
		}
		pos := i.fset.Position(u.pos)
		usage := model.Usage{
			Choice:      u.choice,
			Caller:      u.parent,
			CallerTitle: goTitle(parent.obj),
			File:        pos.Filename,
			Line:        pos.Line,
			Kind:        u.kind,
		}
		if u.call != nil {
			usage.CallID = u.call.id
		}
		usage.Excerpt, usage.ExcerptLine = i.excerpt(
			pos.Filename,
			pos.Line,
			i.fset.Position(parent.decl.Pos()).Line,
			i.fset.Position(parent.decl.End()).Line,
		)
		out = append(out, usage)
	}
	i.mu.RUnlock()

	sort.Slice(out, func(a, b int) bool {
		if out[a].File != out[b].File {
			return out[a].File < out[b].File
		}
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].Kind < out[b].Kind
	})
	return out, nil
}

// excerpt returns up to two lines of context either side of line, clamped
// to [bodyStart, bodyEnd] (the enclosing function), plus the 1-based file
// line the excerpt starts at.
func (i *Indexer) excerpt(file string, line, bodyStart, bodyEnd int) (string, int) {
	buf, err := i.readFile(file)
	if err != nil {
		return "", 0
	}
	lines := strings.Split(string(buf), "\n")
	start := max(line-2, max(bodyStart, 1))
	end := min(line+2, min(bodyEnd, len(lines)))
	if start > end {
		return "", 0
	}
	return strings.Join(lines[start-1:end], "\n"), start
}

// LookupSymbol resolves a symbol name (qualified or unqualified) to a
// target. If multiple match, the first lexicographic FullName wins.
func (i *Indexer) LookupSymbol(name string) (TargetID, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if name == "" {
		return "", fmt.Errorf("empty symbol")
	}

	// A file pseudo-target ("file:<path>") resolves to itself; Frame builds
	// the whole-file view.
	if strings.HasPrefix(name, "file:") {
		return TargetID(name), nil
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
// (case-insensitive). Matches on the leaf name — the method or function name,
// the part after the last "." — rank above matches that only hit the receiver
// type or package path, since a search is almost always for the method/function
// itself. Ranking happens before the limit is applied, so a strong leaf match
// is never dropped in favor of an alphabetically-earlier receiver match.
func (i *Indexer) Search(query string, limit int) []SearchResult {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(query)

	ids := make([]TargetID, 0, len(i.funcs))
	for id := range i.funcs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return string(ids[a]) < string(ids[b]) })

	type hit struct {
		res  SearchResult
		leaf bool // query matched the method/function name, not just the receiver/package
	}
	hits := make([]hit, 0, len(ids))
	for _, id := range ids {
		s := strings.ToLower(string(id))
		if q != "" && !strings.Contains(s, q) {
			continue
		}
		fi := i.funcs[id]
		pos := i.fset.Position(fi.decl.Pos())
		hits = append(hits, hit{
			res: SearchResult{
				TargetID: id,
				Label:    string(id),
				File:     pos.Filename,
				Line:     pos.Line,
			},
			leaf: q == "" || strings.Contains(leafName(s), q),
		})
	}

	// Stable so the alphabetical order within each tier is preserved.
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].leaf && !hits[b].leaf })

	out := make([]SearchResult, 0, limit)
	for _, h := range hits {
		out = append(out, h.res)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// leafName returns the final dot-separated segment of a Go FullName — the bare
// method or function name, without the receiver type or package path. For
// "(github.com/x/pkg.Indexer).Load" it returns "load" (given a lowercased
// input); for "github.com/x/pkg.Validate" it returns "validate".
func leafName(fullName string) string {
	if dot := strings.LastIndex(fullName, "."); dot >= 0 {
		return fullName[dot+1:]
	}
	return fullName
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
