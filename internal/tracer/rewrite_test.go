package tracer

import (
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const fixture = `package fx

import "fmt"

//go:generate echo hi

// Add adds.
func Add(a, b int) int {
	if a == 0 {
		return b
	}
	defer fmt.Println("done")
	for i := 0; i < 2; i++ {
		a++
	}
	switch {
	case b > 0:
		b--
	default:
		b++
	}
	f := func(x int) int {
		return x + 1
	}
	return f(a + b)
}

type T struct{}

func (t *T) M() {}

func (t T) N() { return }

type G[K comparable] struct{}

func (g *G[K]) Get(k K) {}

var handler = func() {
	fmt.Println("global")
}

func noBody()
`

// Every statement in the fixture gets a line call on its own line; function
// bodies open with an enter and a deferred exit; nested literals are named
// after their parent; and nothing moves — the instrumented file has exactly
// the lines the original has.
func TestRewriteInstrumentsStatements(t *testing.T) {
	out, err := Rewrite("fx.go", []byte(fixture), "example.com/m/fx", "example.com/m/rt")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if _, err := parser.ParseFile(token.NewFileSet(), "fx.go", out, parser.ParseComments); err != nil {
		t.Fatalf("rewritten file does not parse: %v\n%s", err, got)
	}
	if strings.Count(got, "\n") != strings.Count(fixture, "\n") {
		t.Errorf("line count changed:\n%s", got)
	}
	if !strings.Contains(got, `package fx; import __unfold_rt "example.com/m/rt"`) {
		t.Errorf("import not spliced after the package clause:\n%s", got)
	}
	// The line call before each statement names the statement's own line.
	lines := strings.Split(got, "\n")
	lineCall := regexp.MustCompile(`__unfold_rt\.Line\(__unfold_h, (\d+)\)`)
	n := 0
	for i, l := range lines {
		for _, m := range lineCall.FindAllStringSubmatch(l, -1) {
			n++
			if m[1] != strconv.Itoa(i+1) {
				t.Errorf("line %d has a call for line %s: %s", i+1, m[1], strings.TrimSpace(l))
			}
		}
	}
	// if, return, defer, for, a++, switch, b--, b++, f :=, return x+1,
	// return f(...), the global literal's Println, and T.N's return.
	if want := 13; n != want {
		t.Errorf("got %d line calls, want %d:\n%s", n, want, got)
	}
	for _, want := range []string{
		`__unfold_h := __unfold_rt.Enter("example.com/m/fx.Add", 8); defer __unfold_rt.Exit(__unfold_h);`,
		`__unfold_rt.Enter("example.com/m/fx.Add.func1", 22)`,
		`__unfold_rt.Enter("(*example.com/m/fx.T).M", 30)`,
		`__unfold_rt.Enter("(example.com/m/fx.T).N", 32)`,
		`__unfold_rt.Enter("(*example.com/m/fx.G[K]).Get", 36)`,
		`__unfold_rt.Enter("example.com/m/fx.glob..func1", 38)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\n%s", want, got)
		}
	}
	if strings.Contains(got, `Enter("example.com/m/fx.noBody"`) {
		t.Errorf("a bodiless declaration was instrumented:\n%s", got)
	}
	// A return in the middle of a function is preceded by its line call and
	// still leaves the deferred exit to fire.
	if !strings.Contains(got, "__unfold_rt.Line(__unfold_h, 10); return b") {
		t.Errorf("early return not instrumented:\n%s", got)
	}
	// Directives and comments are untouched.
	if !strings.Contains(got, "//go:generate echo hi") || !strings.Contains(got, "// Add adds.") {
		t.Errorf("comments were disturbed:\n%s", got)
	}
}

// A file with nothing to instrument is returned as is, without an import
// that would go unused.
func TestRewriteLeavesDeclarationOnlyFilesAlone(t *testing.T) {
	src := "package fx\n\ntype X int\n\nfunc ext()\n"
	out, err := Rewrite("x.go", []byte(src), "p", "rt")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("changed:\n%s", out)
	}
}

// Edits at one offset: a body opening as `{return}` must put the enter
// before the return's line call, or the handle is used before it exists.
func TestRewriteOrdersEditsAtOneOffset(t *testing.T) {
	out, err := Rewrite("x.go", []byte("package fx\n\nfunc f() int {return 1}\n"), "p", "rt")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	enter := strings.Index(got, "Enter(")
	line := strings.Index(got, "Line(")
	if enter < 0 || line < 0 || enter > line {
		t.Errorf("enter must come first:\n%s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", out, 0); err != nil {
		t.Errorf("does not parse: %v\n%s", err, got)
	}
}

// Labels, else-if chains, select and type switches all have statement lists
// the walk must reach, and a label must stay glued to its statement.
func TestRewriteReachesNestedLists(t *testing.T) {
	src := `package fx

func f(ch chan int, v any) {
outer:
	for {
		select {
		case <-ch:
			break outer
		default:
			continue
		}
	}
	if v == nil {
	} else if v == 1 {
		println(1)
	} else {
		println(2)
	}
	switch v.(type) {
	case int:
		println(3)
	}
}
`
	out, err := Rewrite("x.go", []byte(src), "p", "rt")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", out, 0); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, got)
	}
	for _, want := range []string{
		"Line(__unfold_h, 4); outer:\n\tfor {",
		"Line(__unfold_h, 8); break outer",
		"Line(__unfold_h, 10); continue",
		"Line(__unfold_h, 15); println(1)",
		"Line(__unfold_h, 17); println(2)",
		"Line(__unfold_h, 21); println(3)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
