package tsengine

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MaxInertia/unfold/internal/model"
)

// TestGreeterFixture drives the real sidecar (via `bun run main.ts`)
// against the greeter fixture. It is skipped when bun isn't installed so
// CI without the JS toolchain stays green.
func TestGreeterFixture(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not found; skipping TS sidecar integration test")
	}

	_, thisFile, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(thisFile), "..", "..")
	main := filepath.Join(repo, "tsindexer", "main.ts")
	fixture := filepath.Join(repo, "tsindexer", "testdata", "greeter")
	t.Setenv("UNFOLD_TSINDEXER", main)

	e, err := Load(fixture, "./...")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer e.Close()

	// search surfaces the interface impls.
	results := e.Search("greet", 20)
	if len(results) == 0 {
		t.Fatal("search(greet) returned no results")
	}

	// main resolves and contains direct calls to runGreeter.
	mainID, err := e.LookupSymbol("main")
	if err != nil {
		t.Fatalf("LookupSymbol(main): %v", err)
	}
	frame, err := e.Frame(mainID)
	if err != nil {
		t.Fatalf("Frame(main): %v", err)
	}
	if frame.Language != "typescript" {
		t.Errorf("language: got %q want typescript", frame.Language)
	}
	if !strings.Contains(frame.Source, "function main") {
		t.Errorf("main source missing 'function main': %q", frame.Source)
	}

	var runCall *string
	for i := range frame.Calls {
		c := frame.Calls[i]
		if c.DisplayName == "runGreeter" {
			if c.Kind != "direct" || c.TargetID == "" {
				t.Errorf("runGreeter call: kind=%q target=%q, want direct+target", c.Kind, c.TargetID)
			}
			id := string(c.ID)
			runCall = &id
			break
		}
	}
	if runCall == nil {
		t.Fatal("runGreeter call not found in main")
	}

	// Following the direct call lands on runGreeter's body, which dispatches
	// through the Greeter interface (g.greet) — classified as interface.
	callee, err := e.FrameForCall(model.CallID(*runCall), 0)
	if err != nil {
		t.Fatalf("FrameForCall: %v", err)
	}
	if !strings.Contains(callee.Source, "function runGreeter") {
		t.Errorf("callee source missing 'function runGreeter': %q", callee.Source)
	}

	// g.greet dispatches through the Greeter interface and exposes both
	// concrete implementations as candidates.
	var greet *model.CallSite
	for i := range callee.Calls {
		if callee.Calls[i].DisplayName == "g.greet" {
			greet = &callee.Calls[i]
			break
		}
	}
	if greet == nil {
		t.Fatalf("g.greet call not found in runGreeter; calls=%v", callee.Calls)
	}
	if greet.Kind != "interface" {
		t.Errorf("g.greet kind: got %q want interface", greet.Kind)
	}
	if len(greet.Candidates) != 2 {
		t.Fatalf("g.greet candidates: got %d want 2 (English, French): %+v", len(greet.Candidates), greet.Candidates)
	}
	first, err := e.FrameForCall(greet.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall(greet, 0): %v", err)
	}
	second, err := e.FrameForCall(greet.ID, 1)
	if err != nil {
		t.Fatalf("FrameForCall(greet, 1): %v", err)
	}
	if first.ID == second.ID {
		t.Errorf("choice 0 and 1 resolved to the same target %q", first.ID)
	}

	t.Run("utf16-offsets", testUTF16Offsets(e))
}

// testUTF16Offsets verifies call spans are UTF-16 string indices (matching
// what the frontend reads), so a call preceded by multibyte text still
// slices to the function name.
func testUTF16Offsets(e *Engine) func(*testing.T) {
	return func(t *testing.T) {
		waveID, err := e.LookupSymbol("wave")
		if err != nil {
			t.Fatalf("LookupSymbol(wave): %v", err)
		}
		wave, err := e.Frame(waveID)
		if err != nil {
			t.Fatalf("Frame(wave): %v", err)
		}
		var gu *model.CallSite
		for i := range wave.Calls {
			if wave.Calls[i].DisplayName == "greetUnicode" {
				gu = &wave.Calls[i]
				break
			}
		}
		if gu == nil {
			t.Fatalf("greetUnicode call not found in wave; calls=%v", wave.Calls)
		}
		if got := utf16Slice(wave.Source, gu.SpanStart, gu.SpanEnd); got != "greetUnicode" {
			t.Errorf("UTF-16 span sliced to %q, want greetUnicode", got)
		}
	}
}

// TestRxjsFanout checks that an observable .next() is classified as a fan-out
// call whose receivers are the subscribe callbacks, each expandable.
func TestRxjsFanout(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not found; skipping RxJS fan-out integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(thisFile), "..", "..")
	t.Setenv("UNFOLD_TSINDEXER", filepath.Join(repo, "tsindexer", "main.ts"))
	fixture := filepath.Join(repo, "tsindexer", "testdata", "observable")

	e, err := Load(fixture, "./...")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer e.Close()

	id, err := e.LookupSymbol("emit")
	if err != nil {
		t.Fatalf("LookupSymbol(emit): %v", err)
	}
	frame, err := e.Frame(id)
	if err != nil {
		t.Fatalf("Frame(emit): %v", err)
	}

	var fan *model.CallSite
	for i := range frame.Calls {
		if frame.Calls[i].Kind == "fanout" {
			fan = &frame.Calls[i]
			break
		}
	}
	if fan == nil {
		t.Fatalf("no fan-out call in emit; calls=%v", frame.Calls)
	}
	if fan.FanoutKind != "subscribers" {
		t.Errorf("fanoutKind: got %q want subscribers", fan.FanoutKind)
	}
	if len(fan.Receivers) != 2 {
		t.Fatalf("receivers: got %d want 2: %+v", len(fan.Receivers), fan.Receivers)
	}
	for i := range fan.Receivers {
		if fan.Receivers[i].Provenance == "" {
			t.Errorf("receiver %d missing provenance", i)
		}
		body, err := e.FrameForCall(fan.ID, i)
		if err != nil {
			t.Fatalf("FrameForCall(fan, %d): %v", i, err)
		}
		if !strings.Contains(body.Source, "=>") {
			t.Errorf("receiver %d body isn't a callback: %q", i, body.Source)
		}
	}
}

// TestAngularTemplates drives the sidecar against an Angular fixture and
// checks that a component template is indexed as an html Frame whose calls
// resolve to the component's methods, with UTF-16 offsets that survive
// multibyte template text. Skipped when bun is absent.
func TestAngularTemplates(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not found; skipping Angular template integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(thisFile), "..", "..")
	t.Setenv("UNFOLD_TSINDEXER", filepath.Join(repo, "tsindexer", "main.ts"))
	fixture := filepath.Join(repo, "tsindexer", "testdata", "angular")

	e, err := Load(fixture, "./...")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer e.Close()

	// The component template resolves and is an html frame.
	id, err := e.LookupSymbol("AppComponent")
	if err != nil {
		t.Fatalf("LookupSymbol(AppComponent): %v", err)
	}
	tf, err := e.Frame(id)
	if err != nil {
		t.Fatalf("Frame(template): %v", err)
	}
	if tf.Language != "html" {
		t.Errorf("template language: got %q want html", tf.Language)
	}

	var onClick *model.CallSite
	getNameCount := 0
	for i := range tf.Calls {
		c := tf.Calls[i]
		switch c.DisplayName {
		case "onClick":
			onClick = &tf.Calls[i]
		case "getName":
			getNameCount++
			if c.Kind != "direct" || c.TargetID == "" {
				t.Errorf("getName template call unresolved: %+v", c)
			}
			// UTF-16 span must recover the name even after multibyte text.
			if got := utf16Slice(tf.Source, c.SpanStart, c.SpanEnd); got != "getName" {
				t.Errorf("template span sliced to %q, want getName", got)
			}
		}
	}
	if onClick == nil {
		t.Fatalf("onClick call not found in template; calls=%v", tf.Calls)
	}
	if getNameCount < 2 {
		t.Errorf("expected 2 getName calls (incl. one after the emoji), got %d", getNameCount)
	}

	// Expanding a template call lands on the component method body.
	body, err := e.FrameForCall(onClick.ID, 0)
	if err != nil {
		t.Fatalf("FrameForCall(onClick): %v", err)
	}
	if !strings.Contains(body.Source, "onClick(") {
		t.Errorf("onClick expansion missing 'onClick(': %q", body.Source)
	}
}

// utf16Slice slices s by UTF-16 code-unit offsets, the way a JS string
// (and thus the frontend) is indexed.
func utf16Slice(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	if start < 0 || end > len(u) || start > end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}
