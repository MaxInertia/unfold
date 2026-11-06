package traces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveListGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("empty dir lists %v", got)
	}
	tr := Trace{
		Name:    "TestSum",
		Test:    "TestSum",
		Package: "example.com/m/calc",
		Command: "unfold trace -run TestSum ./calc",
		Events: []Event{
			{G: 1, Kind: "enter", Fn: "example.com/m/calc.TestSum", Line: 5, T: 0},
			{G: 1, Kind: "line", Line: 6, T: 10},
			{G: 7, Kind: "enter", Fn: "example.com/m/calc.Background.func1", Line: 28, T: 20},
			{G: 1, Kind: "exit", T: 30},
		},
	}
	path, err := s.Save(tr)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, ".unfold", "traces", "TestSum.json") {
		t.Errorf("path: %s", path)
	}
	// The file is the compact JSON shape with no derived fields.
	raw, _ := os.ReadFile(path)
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "updatedAt", "error"} {
		if _, ok := onDisk[k]; ok {
			t.Errorf("derived field %q written to disk", k)
		}
	}
	if strings.Count(string(raw), "\n") != 1 {
		t.Errorf("not compact: %d lines", strings.Count(string(raw), "\n"))
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	sum := list[0]
	if sum.Name != "TestSum" || sum.Events != 4 || sum.Goroutines != 2 || sum.Package != tr.Package || sum.UpdatedAt == "" {
		t.Errorf("summary: %+v", sum)
	}

	got, err := s.Get("TestSum")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 4 || got.Events[2].Fn != tr.Events[2].Fn || got.Events[2].G != 7 || got.Command != tr.Command {
		t.Errorf("get: %+v", got)
	}
	// Line and exit events carry no function name.
	if strings.Contains(string(raw), `"kind":"line","fn"`) {
		t.Errorf("line events should not carry fn: %s", raw)
	}

	if _, err := s.Get("nope"); err == nil {
		t.Error("Get of a missing trace succeeded")
	}
	if _, err := s.Get("../x"); err == nil {
		t.Error("path-like name accepted")
	}
}

// A file that is not a trace still lists, with the reason, rather than
// vanishing.
func TestMalformedFileListsWithError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	os.MkdirAll(s.Dir(), 0o755)
	os.WriteFile(filepath.Join(s.Dir(), "bad.json"), []byte("{not json"), 0o644)
	os.WriteFile(filepath.Join(s.Dir(), "kind.json"), []byte(`{"package":"p","events":[{"g":1,"kind":"jump"}]}`), 0o644)
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if list[0].Name != "bad" || !strings.Contains(list[0].Error, "not valid JSON") {
		t.Errorf("bad: %+v", list[0])
	}
	if list[1].Name != "kind" || !strings.Contains(list[1].Error, "unknown kind") || list[1].Events != 0 {
		t.Errorf("kind: %+v", list[1])
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		tr   Trace
		want string
	}{
		{"no package", Trace{}, "needs a package"},
		{"ok empty", Trace{Package: "p"}, ""},
		{"enter without fn", Trace{Package: "p", Events: []Event{{Kind: "enter"}}}, "without a function"},
		{"line without line", Trace{Package: "p", Events: []Event{{Kind: "line"}}}, "1-based"},
		{"unpaired is fine", Trace{Package: "p", Events: []Event{{Kind: "exit"}, {Kind: "exit"}}}, ""},
	}
	for _, c := range cases {
		err := Validate(c.tr)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
