package indexer

import (
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

	// Verify span offsets land on the actual call expression text.
	for _, c := range frame.Calls {
		if c.SpanStart < 0 || c.SpanEnd > len(frame.Source) || c.SpanStart >= c.SpanEnd {
			t.Errorf("bad span for %q: [%d,%d) (source len %d)", c.DisplayName, c.SpanStart, c.SpanEnd, len(frame.Source))
			continue
		}
		got := frame.Source[c.SpanStart:c.SpanEnd]
		if !strings.HasSuffix(got, ")") {
			t.Errorf("call span for %q does not end in ')': %q", c.DisplayName, got)
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
	callee, err := idx.FrameForCall(newCall.ID)
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
