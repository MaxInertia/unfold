package indexer

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestErrReturns runs the error-return detector over the errfold fixture and
// checks, block by block, that the bail-out shapes are found with the right
// summary and the control-flow shapes are left alone. Blocks are addressed
// by the trailing comment on their `if` line.
func TestErrReturns(t *testing.T) {
	dir, err := filepath.Abs("testdata/errfold")
	if err != nil {
		t.Fatal(err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id, err := idx.LookupSymbol("shapes")
	if err != nil {
		t.Fatalf("LookupSymbol(shapes): %v", err)
	}
	frame, err := idx.Frame(id)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	lines := strings.Split(frame.Source, "\n")

	// label (the `if` line's comment) -> expected summary. A label missing
	// here must not be reported at all.
	want := map[string]string{
		"plain":                      "returns 0, err",
		"wrapped":                    `wraps "read"`,
		"logged":                     "logs, returns 0, err",
		"init form":                  `wraps "sync"`,
		"panic":                      "panics",
		"errors.Is":                  "returns 0, nil",
		"typed error, not named err": "returns 0, e",
		"bare return in a func with results isn't legal; use a wrap with no literal": "wraps err",
		"fatal": "exits",
		"reports (an HTTP handler's writeError-and-return shape)": "reports, returns 0, err",
	}
	got := map[string]string{}
	for _, r := range frame.ErrReturns {
		if r.StartLine < 0 || r.EndLine >= len(lines) || r.EndLine < r.StartLine {
			t.Errorf("block %+v out of range (source has %d lines)", r, len(lines))
			continue
		}
		ifLine := lines[r.StartLine]
		if !strings.Contains(ifLine, "if ") {
			t.Errorf("block %+v does not start on an if line: %q", r, ifLine)
		}
		if !strings.HasSuffix(strings.TrimSpace(lines[r.EndLine]), "}") {
			t.Errorf("block %+v does not end on a closing brace: %q", r, lines[r.EndLine])
		}
		got[labelOf(ifLine)] = r.Summary
	}
	for label, summary := range want {
		g, ok := got[label]
		if !ok {
			t.Errorf("block %q not detected", label)
			continue
		}
		if g != summary {
			t.Errorf("block %q: summary %q, want %q", label, g, summary)
		}
	}
	for label := range got {
		if _, ok := want[label]; !ok {
			t.Errorf("block %q was folded but must not be", label)
		}
	}

	// The whole-file view carries the same blocks in file-line space, plus
	// main's multi-assign init form.
	fileFrame, err := idx.Frame(TargetID("file:" + filepath.Join(dir, "main.go")))
	if err != nil {
		t.Fatalf("Frame(file:): %v", err)
	}
	fileLines := strings.Split(fileFrame.Source, "\n")
	fileGot := map[string]string{}
	for _, r := range fileFrame.ErrReturns {
		fileGot[labelOf(fileLines[r.StartLine])] = r.Summary
	}
	if fileGot["multi-assign init"] != "exits" {
		t.Errorf("file frame: multi-assign init block: got %q, want %q (all: %v)", fileGot["multi-assign init"], "exits", fileGot)
	}
	if len(fileGot) != len(want)+1 {
		t.Errorf("file frame: %d blocks, want %d: %v", len(fileGot), len(want)+1, fileGot)
	}
	for i := 1; i < len(fileFrame.ErrReturns); i++ {
		if fileFrame.ErrReturns[i].StartLine <= fileFrame.ErrReturns[i-1].StartLine {
			t.Errorf("file frame blocks not sorted by line: %v", fileFrame.ErrReturns)
			break
		}
	}
}

func labelOf(ifLine string) string {
	_, c, ok := strings.Cut(ifLine, "// ")
	if !ok {
		return strings.TrimSpace(ifLine)
	}
	return strings.TrimSpace(c)
}

func TestCleanFormat(t *testing.T) {
	cases := map[string]string{
		"open config: %w":           "open config",
		"read %s: %w":               "read",
		"%w":                        "",
		"decode %q (%d bytes): %v":  "decode ( bytes)",
		"literal %% percent: %w":    "literal % percent",
		"index %[1]s as %-8.3f: %w": "index as",
	}
	for in, want := range cases {
		if got := cleanFormat(in); got != want {
			t.Errorf("cleanFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
