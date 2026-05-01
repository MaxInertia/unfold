package indexer

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadSelf indexes the unfold module itself. It's a sanity check, not
// a contract: it verifies that Load completes without error, finds a
// realistic number of functions and call sites, and that the call kinds
// for a small hand-checked function come out right.
func TestLoadSelf(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(idx.funcs) < 5 {
		t.Errorf("expected >=5 functions in the unfold module, got %d", len(idx.funcs))
	}

	mainID, err := idx.LookupSymbol("github.com/MaxInertia/unfold/cmd/cli.main")
	if err != nil {
		t.Fatalf("LookupSymbol(main): %v", err)
	}
	frame, err := idx.Frame(mainID)
	if err != nil {
		t.Fatalf("Frame(main): %v", err)
	}
	if frame.Language != "go" {
		t.Errorf("frame language: got %q, want go", frame.Language)
	}
	if !strings.Contains(frame.Source, "func main()") {
		t.Errorf("main frame source missing 'func main()' header:\n%s", truncate(frame.Source, 200))
	}
	if len(frame.Calls) < 5 {
		t.Errorf("main has %d call sites; expected at least 5", len(frame.Calls))
	}

	// Span offsets must be in range and must not overlap any other span
	// in the same frame (Shiki's decorations API rejects overlapping
	// ranges). The span text should be a function-name token — i.e. the
	// last segment of the call's display name.
	for i, c := range frame.Calls {
		if c.SpanStart < 0 || c.SpanEnd > len(frame.Source) || c.SpanStart >= c.SpanEnd {
			t.Errorf("bad span for %q: [%d,%d) (source len %d)", c.DisplayName, c.SpanStart, c.SpanEnd, len(frame.Source))
			continue
		}
		got := frame.Source[c.SpanStart:c.SpanEnd]
		// Name-only span means the span text equals either the full display
		// name (for plain identifiers) or the trailing segment after the
		// final "." (for selector calls like fmt.Println).
		dot := strings.LastIndex(c.DisplayName, ".")
		want := c.DisplayName
		if dot >= 0 {
			want = c.DisplayName[dot+1:]
		}
		if got != want {
			t.Errorf("span for %q: got %q, want %q", c.DisplayName, got, want)
		}
		for j, other := range frame.Calls {
			if i == j {
				continue
			}
			if c.SpanStart < other.SpanEnd && other.SpanStart < c.SpanEnd {
				t.Errorf("overlap: %q [%d,%d) overlaps %q [%d,%d)",
					c.DisplayName, c.SpanStart, c.SpanEnd,
					other.DisplayName, other.SpanStart, other.SpanEnd)
			}
		}
	}

	// indexer.New() in main is package-level — must be direct and resolvable.
	if !findCall(frame.Calls, func(c CallSite) bool {
		return c.DisplayName == "indexer.New" && c.Kind == KindDirect && c.TargetID != ""
	}) {
		t.Errorf("expected a direct call to indexer.New in main; calls=%v", callSummary(frame.Calls))
	}

	// idx.Load — method on concrete *Indexer, must resolve direct.
	if !findCall(frame.Calls, func(c CallSite) bool {
		return c.DisplayName == "idx.Load" && c.Kind == KindDirect && c.TargetID != ""
	}) {
		t.Errorf("expected a direct call to idx.Load in main; calls=%v", callSummary(frame.Calls))
	}
}

// TestFrameForCall verifies that following a direct call from main lands
// on the callee's frame.
func TestFrameForCall(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mainID, err := idx.LookupSymbol("github.com/MaxInertia/unfold/cmd/cli.main")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	mainFrame, err := idx.Frame(mainID)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}

	// Find indexer.New() and follow it.
	var newCall *CallSite
	for i, c := range mainFrame.Calls {
		if c.DisplayName == "indexer.New" {
			newCall = &mainFrame.Calls[i]
			break
		}
	}
	if newCall == nil {
		t.Fatal("indexer.New call not found in main")
	}
	callee, err := idx.FrameForCall(newCall.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall: %v", err)
	}
	if !strings.Contains(callee.Source, "func New()") {
		t.Errorf("callee source missing 'func New()': %s", truncate(callee.Source, 150))
	}
	if !strings.Contains(string(callee.ID), "indexer.New") {
		t.Errorf("callee target id %q doesn't include indexer.New", callee.ID)
	}
}

// TestInterfaceCandidates loads a small fixture with one interface
// (Greeter) and two concrete impls (English, French), then verifies
// that the call to g.Greet inside RunGreeter is classified as
// interface and exposes both candidates.
func TestInterfaceCandidates(t *testing.T) {
	dir, err := filepath.Abs("testdata/diapp")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}

	runID, err := idx.LookupSymbol("RunGreeter")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	frame, err := idx.Frame(runID)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}

	var greetCall *CallSite
	for i := range frame.Calls {
		if frame.Calls[i].DisplayName == "g.Greet" {
			greetCall = &frame.Calls[i]
			break
		}
	}
	if greetCall == nil {
		t.Fatalf("g.Greet call not found in RunGreeter; calls=%v", callSummary(frame.Calls))
	}
	if greetCall.Kind != KindInterface {
		t.Errorf("g.Greet kind: got %s, want interface", greetCall.Kind)
	}
	if len(greetCall.Candidates) != 2 {
		t.Errorf("expected 2 candidates (English, French), got %d: %+v", len(greetCall.Candidates), greetCall.Candidates)
	}
	labels := []string{}
	for _, c := range greetCall.Candidates {
		labels = append(labels, c.Label)
	}
	want := []string{"English", "French"}
	for _, w := range want {
		found := false
		for _, l := range labels {
			if strings.Contains(l, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("candidate label list missing %q: %v", w, labels)
		}
	}

	// FrameForCall with choice 0 and 1 should return different bodies.
	first, err := idx.FrameForCall(greetCall.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall(choice=0): %v", err)
	}
	second, err := idx.FrameForCall(greetCall.ID, 1)
	if err != nil {
		t.Fatalf("FrameForCall(choice=1): %v", err)
	}
	if first.ID == second.ID {
		t.Errorf("expected different targets for choice 0 vs 1; both=%s", first.ID)
	}

	// Out-of-range choice clamps to 0.
	clamped, err := idx.FrameForCall(greetCall.ID, 99)
	if err != nil {
		t.Fatalf("FrameForCall(choice=99): %v", err)
	}
	if clamped.ID != first.ID {
		t.Errorf("choice=99 should clamp to 0; got %s, want %s", clamped.ID, first.ID)
	}
}

func findCall(calls []CallSite, pred func(CallSite) bool) bool {
	for _, c := range calls {
		if pred(c) {
			return true
		}
	}
	return false
}

func callSummary(calls []CallSite) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, string(c.Kind)+":"+c.DisplayName)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
