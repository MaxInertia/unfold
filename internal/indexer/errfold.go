package indexer

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

// errReturns finds the `if err != nil { … }` blocks under body whose body only
// bails out. A reader skips these on the way to the happy path, which is
// exactly why the frontend offers to fold them all at once — but a block that
// does anything else (retries, falls back, mutates state) is control flow the
// reader must see, so it is left alone.
//
// Line indices are relative to baseLine (the frame's first line), matching the
// 0-based Source space the frontend renders rows in.
func errReturns(fset *token.FileSet, info *types.Info, body ast.Node, baseLine int) []model.ErrReturn {
	if body == nil {
		return nil
	}
	var out []model.ErrReturn
	ast.Inspect(body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// An else branch means the if carries both outcomes; hiding the
		// error half would leave the else dangling off nothing.
		if ifs.Else != nil || !isErrCond(info, ifs.Cond) {
			return true
		}
		summary, ok := bailoutSummary(info, ifs.Body)
		if !ok {
			return true
		}
		out = append(out, model.ErrReturn{
			StartLine: fset.Position(ifs.Pos()).Line - baseLine,
			EndLine:   fset.Position(ifs.End()).Line - baseLine,
			Summary:   summary,
		})
		// Nothing inside a pure bail-out can be another block worth folding
		// on its own (and nesting one fold inside another would make the
		// frontend's hidden-row bookkeeping ambiguous).
		return false
	})
	return out
}

// isErrCond reports whether cond is an error check: `err != nil`, an
// `errors.Is`/`errors.As` call, or those joined by && / || / !. The operand
// qualifies by type when go/types resolved it to `error`, and by the `err`
// naming convention when it didn't (a partially-typed package still reads
// the same to a human).
func isErrCond(info *types.Info, cond ast.Expr) bool {
	switch e := cond.(type) {
	case *ast.ParenExpr:
		return isErrCond(info, e.X)
	case *ast.UnaryExpr:
		return e.Op == token.NOT && isErrCond(info, e.X)
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR:
			// `err != nil && !errors.Is(err, io.EOF)` is still an error
			// check; one qualifying side is enough because the other only
			// narrows it.
			return isErrCond(info, e.X) || isErrCond(info, e.Y)
		case token.NEQ:
			if isNil(e.Y) {
				return isErrorValue(info, e.X)
			}
			if isNil(e.X) {
				return isErrorValue(info, e.Y)
			}
		}
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "errors" {
				return sel.Sel.Name == "Is" || sel.Sel.Name == "As"
			}
		}
	}
	return false
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// isErrorValue reports whether e is an error: by type when known, else by
// the `err` / `xxxErr` naming convention.
func isErrorValue(info *types.Info, e ast.Expr) bool {
	if info != nil {
		if t := info.TypeOf(e); t != nil {
			if types.Identical(t, types.Universe.Lookup("error").Type()) {
				return true
			}
			// A named error type (`*MyErr`) compared to nil reads the same.
			if types.Implements(t, types.Universe.Lookup("error").Type().Underlying().(*types.Interface)) {
				return true
			}
			// Typed and not an error — the name is not allowed to overrule
			// go/types here.
			return false
		}
	}
	name := ""
	switch x := e.(type) {
	case *ast.Ident:
		name = x.Name
	case *ast.SelectorExpr:
		name = x.Sel.Name
	}
	return name == "err" || strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "err")
}

// bailoutSummary decides whether blk only leaves the function, and if so says
// how. Allowed: zero or more logging calls, then exactly one terminal
// statement — a return, a panic, or an exit (os.Exit, log.Fatal*, t.Fatal*).
// Anything else makes the block real control flow and disqualifies it.
func bailoutSummary(info *types.Info, blk *ast.BlockStmt) (string, bool) {
	if blk == nil || len(blk.List) == 0 {
		return "", false
	}
	// "logs" wins over "reports" when both appear: it is the word a reader
	// expects, and the distinction is only there so an HTTP handler's
	// `writeError(w, …); return` reads as what it is.
	prefix := ""
	for _, st := range blk.List[:len(blk.List)-1] {
		kind := reportKind(st)
		if kind == "" {
			return "", false
		}
		if prefix == "" || kind == "logs" {
			prefix = kind
		}
	}
	var summary string
	switch last := blk.List[len(blk.List)-1].(type) {
	case *ast.ReturnStmt:
		summary = returnSummary(info, last)
	case *ast.ExprStmt:
		call, ok := last.X.(*ast.CallExpr)
		if !ok {
			return "", false
		}
		switch {
		case isIdentCall(call, "panic"):
			summary = "panics"
			if len(call.Args) == 1 {
				if lit := litString(call.Args[0]); lit != "" {
					summary = "panics " + strconv.Quote(lit)
				}
			}
		case isFatalCall(call):
			summary = "exits"
		default:
			return "", false
		}
	default:
		return "", false
	}
	if prefix != "" {
		summary = prefix + ", " + summary
	}
	return summary, true
}

// returnSummary renders what a return hands back. A wrapping call is the
// common shape, and the message is the part worth reading — `wraps "open
// config"` says more than `returns nil, fmt.Errorf(...)` would.
func returnSummary(info *types.Info, ret *ast.ReturnStmt) string {
	if len(ret.Results) == 0 {
		return "returns"
	}
	for _, r := range ret.Results {
		if call, ok := r.(*ast.CallExpr); ok {
			if msg, ok := wrapMessage(call); ok {
				if msg == "" {
					return "wraps err"
				}
				return "wraps " + strconv.Quote(msg)
			}
		}
	}
	parts := make([]string, 0, len(ret.Results))
	for _, r := range ret.Results {
		parts = append(parts, exprShort(r))
	}
	return "returns " + strings.Join(parts, ", ")
}

// wrapMessage recognises the calls that wrap an error with a message —
// fmt.Errorf, errors.Wrap/Wrapf (pkg/errors and lookalikes), errors.Join —
// and returns the message with its verbs stripped. The bool says whether the
// call wraps at all; a wrap with no literal message reports "".
func wrapMessage(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch {
	case pkg.Name == "fmt" && sel.Sel.Name == "Errorf":
		if len(call.Args) == 0 {
			return "", false
		}
		return cleanFormat(litString(call.Args[0])), true
	case pkg.Name == "errors" && (sel.Sel.Name == "Wrap" || sel.Sel.Name == "Wrapf" || sel.Sel.Name == "WithMessage" || sel.Sel.Name == "WithMessagef"):
		if len(call.Args) < 2 {
			return "", true
		}
		return cleanFormat(litString(call.Args[1])), true
	case pkg.Name == "errors" && (sel.Sel.Name == "Join" || sel.Sel.Name == "WithStack"):
		return "", true
	}
	return "", false
}

// cleanFormat turns a format string into the human part of the message:
// `open %s: %w` → `open: …`-style noise is dropped by removing verbs and the
// separators left dangling around them. "open config: %w" → "open config".
func cleanFormat(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		// Skip the verb: flags, width, precision, then the verb letter.
		j := i + 1
		for j < len(s) && strings.IndexByte("+-# 0123456789.[]*", s[j]) >= 0 {
			j++
		}
		if j < len(s) && s[j] == '%' {
			b.WriteByte('%')
		}
		i = j
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimRight(out, ":;,- ")
	out = strings.Join(strings.Fields(out), " ")
	if len(out) > 48 {
		out = out[:47] + "…"
	}
	return out
}

// reportKind classifies a statement that only tells someone about the error
// before the block leaves: "logs" for a call whose name says log/print/warn/
// debug (zap, slog, logrus, zerolog's .Msg(), plain log.Printf — matching
// the name rather than the logger type keeps them all in), "reports" for a
// call whose name carries the error outward some other way — `writeError(w,
// …)`, `http.Error(w, …)`, `respondErr(…)`, `t.Errorf(…)`. Anything else is
// "" and disqualifies the block: it is doing work, not reporting.
func reportKind(st ast.Stmt) string {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return ""
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return ""
	}
	lower := strings.ToLower(calleeName(call))
	for _, p := range []string{"log", "print", "debug", "info", "warn", "trace", "msg", "record", "report", "capture"} {
		if strings.HasPrefix(lower, p) {
			return "logs"
		}
	}
	if strings.Contains(lower, "err") || strings.Contains(lower, "fail") {
		return "reports"
	}
	return ""
}

// isFatalCall matches calls that never return: os.Exit, log.Fatal*, and
// testing's t.Fatal*/t.FailNow/t.Skip*.
func isFatalCall(call *ast.CallExpr) bool {
	name := calleeName(call)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "os" && name == "Exit" {
			return true
		}
	}
	return strings.HasPrefix(name, "Fatal") || name == "FailNow" || strings.HasPrefix(name, "Skip")
}

func isIdentCall(call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == name
}

// calleeName is the last identifier of the callee: `Fatalf` for both
// `log.Fatalf` and `t.Fatalf`, `Errorf` for `fmt.Errorf`.
func calleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.IndexExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// litString unquotes a string literal, or returns "" for anything else.
func litString(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// exprShort prints an expression for a one-line summary: identifiers and
// literals as written, anything longer as an ellipsis so the summary stays a
// glance, not a second copy of the code.
func exprShort(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		if len(x.Value) <= 16 {
			return x.Value
		}
	case *ast.SelectorExpr, *ast.UnaryExpr, *ast.CompositeLit:
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, token.NewFileSet(), e); err == nil && buf.Len() <= 24 {
			return buf.String()
		}
	}
	return "…"
}
