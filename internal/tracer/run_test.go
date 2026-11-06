package tracer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The whole path, on a throwaway module: instrument, build through the
// overlay, run one test, read the events back. The order of events is the
// claim the replay rests on, so it is checked exactly, including a call made
// through a method, a closure, an early return and a second goroutine.
func TestRunRecordsExecutionOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module with the go tool")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.25\n")
	write("calc/calc.go", `package calc

type Acc struct{ n int }

func (a *Acc) Add(v int) {
	if v < 0 {
		return
	}
	a.n += v
}

func Sum(vs []int) int {
	a := &Acc{}
	each(vs, func(v int) {
		a.Add(v)
	})
	return a.n
}

func each(vs []int, f func(int)) {
	for _, v := range vs {
		f(v)
	}
}

func Background() {
	done := make(chan bool)
	go func() {
		done <- true
	}()
	<-done
}
`)
	write("calc/calc_test.go", `package calc

import "testing"

func TestSum(t *testing.T) {
	if Sum([]int{2, -1}) != 2 {
		t.Fatal("bad sum")
	}
	Background()
}

func TestOther(t *testing.T) {
	Sum(nil)
}
`)

	tr, err := Run(Options{Dir: dir, Pattern: "./calc", Run: "TestSum", Output: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Package != "example.com/m/calc" || tr.Test != "TestSum" {
		t.Errorf("package/test: %q %q", tr.Package, tr.Test)
	}
	if tr.Truncated {
		t.Error("truncated")
	}
	// The test goroutine's events, rendered compactly: enters by the name
	// after the last slash, lines by number, exits as ^.
	main := tr.Events[0].G
	var seq []string
	others := 0
	for _, ev := range tr.Events {
		if ev.G != main {
			others++
			continue
		}
		switch ev.Kind {
		case "enter":
			seq = append(seq, ev.Fn[strings.LastIndex(ev.Fn, "/")+1:])
		case "line":
			seq = append(seq, strconv.Itoa(ev.Line))
		case "exit":
			seq = append(seq, "^")
		}
	}
	got := strings.Join(seq, " ")
	want := strings.Join([]string{
		"calc.TestSum", "6",
		"calc.Sum", "13", "14",
		"calc.each", "21", "22",
		"calc.Sum.func1", "15", "calc.Acc).Add", "6", "9", "^", "^",
		"22",
		"calc.Sum.func1", "15", "calc.Acc).Add", "6", "7", "^", "^",
		"^",       // each
		"17", "^", // Sum
		"9", "calc.Background", "27", "28", "31", "^",
		"^", // TestSum
	}, " ")
	if got != want {
		t.Errorf("event order\n got: %s\nwant: %s", got, want)
	}
	// The goroutine Background started is recorded under its own id.
	if others != 3 {
		t.Errorf("other-goroutine events: got %d want 3 (enter, line, exit)", others)
	}
	// -run selected one test.
	for _, ev := range tr.Events {
		if strings.HasSuffix(ev.Fn, "TestOther") {
			t.Error("TestOther ran")
		}
	}
	// Nothing in the checkout changed.
	src, _ := os.ReadFile(filepath.Join(dir, "calc", "calc.go"))
	if strings.Contains(string(src), "__unfold") {
		t.Error("source file was modified")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("module root gained files: %v", entries)
	}
}

// The cap stops the recording and says so.
func TestRunTruncates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module with the go tool")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package m\n\nfunc Loop() {\n\tn := 0\n\tfor i := 0; i < 100; i++ {\n\t\tn++\n\t}\n\t_ = n\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package m\n\nimport \"testing\"\n\nfunc TestLoop(t *testing.T) { Loop() }\n"), 0o644)
	tr, err := Run(Options{Dir: dir, Pattern: ".", MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Truncated || len(tr.Events) != 10 {
		t.Errorf("truncated=%v events=%d", tr.Truncated, len(tr.Events))
	}
}
