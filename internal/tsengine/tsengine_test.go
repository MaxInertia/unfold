package tsengine

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	foundInterface := false
	for _, c := range callee.Calls {
		if c.DisplayName == "g.greet" && c.Kind == "interface" {
			foundInterface = true
		}
	}
	if !foundInterface {
		t.Errorf("expected interface call g.greet in runGreeter; calls=%v", callee.Calls)
	}
}
