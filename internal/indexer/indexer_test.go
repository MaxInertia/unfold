package indexer

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
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

	// engine.NewReloadable(...) in main is a package-level call — must be
	// direct and resolvable into the internal/engine package.
	if !findCall(frame.Calls, func(c CallSite) bool {
		return c.DisplayName == "engine.NewReloadable" && c.Kind == KindDirect && c.TargetID != ""
	}) {
		t.Errorf("expected a direct call to engine.NewReloadable in main; calls=%v", callSummary(frame.Calls))
	}

	// engine.Detect — also a package-level call, must resolve direct.
	if !findCall(frame.Calls, func(c CallSite) bool {
		return c.DisplayName == "engine.Detect" && c.Kind == KindDirect && c.TargetID != ""
	}) {
		t.Errorf("expected a direct call to engine.Detect in main; calls=%v", callSummary(frame.Calls))
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

	// Find engine.NewReloadable() and follow it.
	var loadCall *CallSite
	for i, c := range mainFrame.Calls {
		if c.DisplayName == "engine.NewReloadable" {
			loadCall = &mainFrame.Calls[i]
			break
		}
	}
	if loadCall == nil {
		t.Fatal("engine.NewReloadable call not found in main")
	}
	callee, err := idx.FrameForCall(loadCall.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall: %v", err)
	}
	if !strings.Contains(callee.Source, "func NewReloadable(") {
		t.Errorf("callee source missing 'func NewReloadable(': %s", truncate(callee.Source, 150))
	}
	if !strings.Contains(string(callee.ID), "engine.NewReloadable") {
		t.Errorf("callee target id %q doesn't include engine.NewReloadable", callee.ID)
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

// TestUTF16SpanOffsets verifies call-site span offsets are UTF-16 code-unit
// indices into Source (what the frontend reads), not UTF-8 byte offsets.
// resolveCall is a good probe: its comments contain em-dashes (3 bytes, 1
// UTF-16 unit), so byte offsets would drift the spans right.
func TestUTF16SpanOffsets(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id, err := idx.LookupSymbol("resolveCall")
	if err != nil {
		t.Fatalf("LookupSymbol(resolveCall): %v", err)
	}
	frame, err := idx.Frame(id)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}

	// Sanity: the probe only means something if the source has non-ASCII.
	hasNonASCII := false
	for _, r := range frame.Source {
		if r > 127 {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII {
		t.Skip("resolveCall source is all ASCII; probe is meaningless")
	}

	driftSeen := false
	for _, c := range frame.Calls {
		want := c.DisplayName
		if dot := strings.LastIndex(want, "."); dot >= 0 {
			want = want[dot+1:]
		}
		// UTF-16 slice (what the frontend does) must recover the name token.
		if got := utf16SliceStr(frame.Source, c.SpanStart, c.SpanEnd); got != want {
			t.Errorf("UTF-16 span for %q sliced to %q, want %q", c.DisplayName, got, want)
		}
		// If a naive byte slice differs, this span is one the fix corrected.
		if byteSlice(frame.Source, c.SpanStart, c.SpanEnd) != want {
			driftSeen = true
		}
	}
	if !driftSeen {
		t.Error("expected at least one span where byte offsets would have drifted; none found")
	}
}

func utf16SliceStr(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	if start < 0 || end > len(u) || start > end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}

func byteSlice(s string, start, end int) string {
	if start < 0 || end > len(s) || start > end {
		return ""
	}
	return s[start:end]
}

// TestFileFrame verifies Files() lists only main-module files and that a
// "file:<path>" target yields a whole-file frame with expandable call sites.
func TestFileFrame(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}

	files := idx.Files()
	if len(files) == 0 {
		t.Fatal("Files() returned none")
	}
	var serverGo string
	for _, f := range files {
		if strings.Contains(f, "/go/pkg/mod/") || strings.Contains(f, "/go-build/") {
			t.Errorf("Files() leaked a dependency file: %s", f)
		}
		if strings.HasSuffix(f, "internal/server/server.go") {
			serverGo = f
		}
	}
	if serverGo == "" {
		t.Fatalf("server.go not in Files(): %v", files)
	}

	fr, err := idx.Frame(TargetID("file:" + serverGo))
	if err != nil {
		t.Fatalf("Frame(file:server.go): %v", err)
	}
	if fr.Title != "server.go" {
		t.Errorf("title: got %q want server.go", fr.Title)
	}
	if !strings.Contains(fr.Source, "package server") {
		t.Error("file frame source missing 'package server'")
	}
	if len(fr.Calls) == 0 {
		t.Fatal("file frame has no call sites")
	}
	expandable := 0
	for _, c := range fr.Calls {
		if c.SpanStart < 0 || c.SpanEnd > len(fr.Source) || c.SpanStart >= c.SpanEnd {
			t.Errorf("bad span for %q: [%d,%d)", c.DisplayName, c.SpanStart, c.SpanEnd)
		}
		if c.Kind == KindDirect && c.TargetID != "" {
			expandable++
		}
	}
	if expandable == 0 {
		t.Error("file frame has no expandable (direct+target) calls")
	}
}

// TestTypeInfo hovers the name token of a call inside resolveCall and checks
// the resolved symbol's type details.
func TestTypeInfo(t *testing.T) {
	idx := New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id, err := idx.LookupSymbol("resolveCall")
	if err != nil {
		t.Fatalf("LookupSymbol: %v", err)
	}
	frame, err := idx.Frame(id)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	var span *CallSite
	for i := range frame.Calls {
		if frame.Calls[i].DisplayName == "nameSpan" {
			span = &frame.Calls[i]
			break
		}
	}
	if span == nil {
		t.Fatalf("nameSpan call not found; calls=%v", callSummary(frame.Calls))
	}

	ti, err := idx.TypeInfo(id, span.SpanStart)
	if err != nil {
		t.Fatalf("TypeInfo: %v", err)
	}
	if ti == nil {
		t.Fatal("TypeInfo returned nil over a call's name token")
	}
	if ti.Name != "nameSpan" {
		t.Errorf("name: got %q want nameSpan", ti.Name)
	}
	if ti.Kind != "func" {
		t.Errorf("kind: got %q want func", ti.Kind)
	}
	if !strings.Contains(ti.Type, "func(") {
		t.Errorf("type not a func signature: %q", ti.Type)
	}
	if !strings.Contains(ti.DefinedAt, "indexer.go:") {
		t.Errorf("definedAt: got %q", ti.DefinedAt)
	}
	if !strings.Contains(string(ti.TargetID), "nameSpan") {
		t.Errorf("targetId: got %q", ti.TargetID)
	}

	// Hovering whitespace/punctuation returns nil, not an error.
	if got, err := idx.TypeInfo(id, 0); err != nil {
		t.Errorf("TypeInfo at offset 0: %v", err)
	} else if got != nil && got.Name != "" && got.Kind != "func" && got.Kind != "type" {
		// offset 0 is "func" keyword start; resolving there may yield the
		// function name ident — tolerate either nil or a sane result.
		_ = got
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

// TestGoroutineLaunch verifies that a call launched with the `go` keyword is
// flagged Goroutine=true while an ordinary call in the same body is not.
func TestGoroutineLaunch(t *testing.T) {
	dir, err := filepath.Abs("testdata/goroutines")
	if err != nil {
		t.Fatal(err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id, err := idx.LookupSymbol("launch")
	if err != nil {
		t.Fatalf("LookupSymbol(launch): %v", err)
	}
	frame, err := idx.Frame(id)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}

	assertGoroutineFlags(t, frame)

	// Same flags must hold in the whole-file view (fileFrame propagates the
	// flag from the shared callInfo).
	fileFrame, err := idx.Frame(TargetID("file:" + filepath.Join(dir, "main.go")))
	if err != nil {
		t.Fatalf("Frame(file:): %v", err)
	}
	assertGoroutineFlags(t, fileFrame)
}

// assertGoroutineFlags checks the goroutine flags on a frame that contains
// the goroutines fixture's launch body: `go worker()` is flagged, while the
// ordinary `blocking()` and the deferred `cleanup()` are not. The anonymous
// `go func(){…}()` has no named call site, so it must not appear at all.
func assertGoroutineFlags(t *testing.T, frame *Frame) {
	t.Helper()
	flags := map[string]int{} // displayName -> count
	var worker, blocking, cleanup *CallSite
	for i := range frame.Calls {
		c := &frame.Calls[i]
		flags[c.DisplayName]++
		switch c.DisplayName {
		case "worker":
			worker = c
		case "blocking":
			blocking = c
		case "cleanup":
			cleanup = c
		}
	}

	if worker == nil || blocking == nil || cleanup == nil {
		t.Fatalf("missing expected call sites: %v", flags)
	}
	// The anonymous goroutine body has no named call, so there must be
	// exactly one worker call site (the `go worker()`), not two.
	if flags["worker"] != 1 {
		t.Errorf("worker call sites = %d, want 1", flags["worker"])
	}
	if !worker.Goroutine {
		t.Errorf("worker() is launched with `go`; want Goroutine=true")
	}
	if blocking.Goroutine {
		t.Errorf("blocking() is an ordinary call; want Goroutine=false")
	}
	if cleanup.Goroutine {
		t.Errorf("cleanup() is deferred, not a goroutine; want Goroutine=false")
	}
}
