// Package tracer records what one run of a Go test actually did, as the
// ordered enter / line / exit events the UI replays through the unfolded
// code. Go has no cheap line-level execution trace — coverage counts blocks
// without order, runtime/trace has no lines — so the package is built
// instrumented: every function body gets an enter/exit pair and every
// statement a line call, the build reads the rewritten files through a
// `go test -overlay` so the checkout is never touched, and the calls write
// to a file the CLI turns into a trace.
package tracer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Identifiers the instrumentation introduces. Double-underscored so they
// cannot collide with anything a person wrote, and the handle is declared
// per function so a nested literal shadows rather than shares its parent's.
const (
	rtAlias = "__unfold_rt"
	handle  = "__unfold_h"
)

// Rewrite instruments one source file. pkgPath is the import path the
// functions are named under (the package's, or <pkg>_test for an external
// test file) and rtImport the runtime package's import path.
//
// The rewrite is textual: the AST supplies the positions, and the calls are
// spliced into the original bytes on the same line they instrument. Printing
// a modified AST would reflow the file and lose the comment placement that
// //go:embed and //go:build depend on; splicing keeps every byte and every
// line number — a panic in an instrumented run still points at the real
// line, and the events do too.
func Rewrite(filename string, src []byte, pkgPath, rtImport string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	rw := &rewriter{fset: fset, pkgPath: pkgPath}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			rw.instrument(d.Body, funcName(pkgPath, d), rw.line(d.Pos()))
		case *ast.GenDecl:
			// Package-level literals (var handler = func() {...}) are named the
			// way the Go runtime names them.
			rw.literals(d, pkgPath+".glob..func")
		}
	}
	if len(rw.edits) == 0 {
		return src, nil
	}
	// The import goes right after the package clause, which is the one place
	// that is legal before whatever the file declares first.
	rw.insert(f.Name.End(), fmt.Sprintf("; import %s %q", rtAlias, rtImport))
	return rw.apply(src), nil
}

type edit struct {
	off  int
	text string
}

type rewriter struct {
	fset    *token.FileSet
	pkgPath string
	edits   []edit
}

func (r *rewriter) line(p token.Pos) int { return r.fset.Position(p).Line }

func (r *rewriter) insert(p token.Pos, text string) {
	r.edits = append(r.edits, edit{off: r.fset.Position(p).Offset, text: text})
}

// instrument wires one function body: the handle and its deferred exit at
// the opening brace, a line call before every statement, and each function
// literal inside it as a function of its own.
func (r *rewriter) instrument(body *ast.BlockStmt, name string, declLine int) {
	r.insert(body.Lbrace+1, fmt.Sprintf(" %s := %s.Enter(%q, %d); defer %s.Exit(%s);",
		handle, rtAlias, name, declLine, rtAlias, handle))
	r.stmts(body.List)
	r.literals(body, name+".func")
}

// literals instruments every function literal under n that is not nested
// in another literal (those are reached when their parent is). Numbering
// follows the runtime's: Outer.func1 for a literal in a declaration, and
// Outer.func1.1 for one inside that literal — `prefix` already ends in
// "func" or "." accordingly.
func (r *rewriter) literals(n ast.Node, prefix string) {
	i := 0
	ast.Inspect(n, func(x ast.Node) bool {
		lit, ok := x.(*ast.FuncLit)
		if !ok {
			return true
		}
		i++
		name := prefix + strconv.Itoa(i)
		r.insert(lit.Body.Lbrace+1, fmt.Sprintf(" %s := %s.Enter(%q, %d); defer %s.Exit(%s);",
			handle, rtAlias, name, r.line(lit.Pos()), rtAlias, handle))
		r.stmts(lit.Body.List)
		r.literals(lit.Body, name+".")
		return false
	})
}

// stmts puts a line call before each statement of a list and descends into
// the statement lists nested in it. Only lists: an if's condition or a
// for's post statement is not a place a statement can be inserted.
func (r *rewriter) stmts(list []ast.Stmt) {
	for _, s := range list {
		if _, empty := s.(*ast.EmptyStmt); empty {
			continue
		}
		r.insert(s.Pos(), fmt.Sprintf("%s.Line(%s, %d); ", rtAlias, handle, r.line(s.Pos())))
		r.nested(s)
	}
}

func (r *rewriter) nested(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		r.stmts(s.List)
	case *ast.IfStmt:
		r.stmts(s.Body.List)
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			r.stmts(e.List)
		case *ast.IfStmt:
			// `else if`: no statement list to put a line call in, but its
			// own body has one.
			r.nested(e)
		}
	case *ast.ForStmt:
		r.stmts(s.Body.List)
	case *ast.RangeStmt:
		r.stmts(s.Body.List)
	case *ast.SwitchStmt:
		r.clauses(s.Body.List)
	case *ast.TypeSwitchStmt:
		r.clauses(s.Body.List)
	case *ast.SelectStmt:
		r.clauses(s.Body.List)
	case *ast.LabeledStmt:
		// Nothing may come between a label and its statement, so the
		// labeled statement got the line call and its body is all that is
		// left to do.
		r.nested(s.Stmt)
	}
}

func (r *rewriter) clauses(list []ast.Stmt) {
	for _, c := range list {
		switch c := c.(type) {
		case *ast.CaseClause:
			r.stmts(c.Body)
		case *ast.CommClause:
			r.stmts(c.Body)
		}
	}
}

// apply splices the edits into src. Later offsets go first so earlier ones
// stay valid; edits at one offset keep the order they were added in, which
// is how the enter lands before the first statement's line call when the
// body opens as `{return}`.
func (r *rewriter) apply(src []byte) []byte {
	idx := make([]int, len(r.edits))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := r.edits[idx[a]], r.edits[idx[b]]
		if ea.off != eb.off {
			return ea.off > eb.off
		}
		return idx[a] > idx[b]
	})
	out := make([]byte, len(src))
	copy(out, src)
	for _, i := range idx {
		e := r.edits[i]
		out = append(out[:e.off], append([]byte(e.text), out[e.off:]...)...)
	}
	return out
}

// funcName is the go/types FullName of a declaration — "path/pkg.F",
// "(*path/pkg.T).M", "(path/pkg.T).M" — built from syntax alone, so the
// instrumented program names functions the way the index keys them without
// the rewrite having to type-check anything.
func funcName(pkgPath string, d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return pkgPath + "." + d.Name.Name
	}
	t := d.Recv.List[0].Type
	ptr := false
	if s, ok := t.(*ast.StarExpr); ok {
		ptr = true
		t = s.X
	}
	recv := typeName(t)
	if ptr {
		return "(*" + pkgPath + "." + recv + ")." + d.Name.Name
	}
	return "(" + pkgPath + "." + recv + ")." + d.Name.Name
}

// typeName renders a receiver's type expression: T, or T[K, V] for a
// generic receiver, matching how types.TypeString writes the instantiation.
func typeName(t ast.Expr) string {
	switch t := t.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return typeName(t.X) + "[" + typeName(t.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, len(t.Indices))
		for i, e := range t.Indices {
			parts[i] = typeName(e)
		}
		return typeName(t.X) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.ParenExpr:
		return typeName(t.X)
	}
	return "?"
}
