package tracer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MaxInertia/unfold/internal/traces"
)

// Options says what to trace.
type Options struct {
	// Dir is the directory the package pattern is relative to (empty: the
	// working directory). The module it belongs to is found from there.
	Dir string
	// Pattern names exactly one package, as `go test` takes it: ./internal/x.
	Pattern string
	// Run is passed to `go test -run`; empty runs every test in the package.
	Run string
	// MaxEvents caps the recording; zero means the runtime's default.
	MaxEvents int
	// Output receives the test's own output. Nil discards it.
	Output io.Writer
}

// Run builds the package with every function instrumented, runs its tests,
// and returns what they executed. The checkout is not modified: the
// rewritten files, and the runtime package they call, reach the compiler
// through a `go test -overlay`.
func Run(opts Options) (traces.Trace, error) {
	pkg, err := listPackage(opts.Dir, opts.Pattern)
	if err != nil {
		return traces.Trace{}, err
	}
	tmp, err := os.MkdirTemp("", "unfold-trace-")
	if err != nil {
		return traces.Trace{}, err
	}
	defer os.RemoveAll(tmp)

	// The runtime lives at a path under the module root so the instrumented
	// package can import it by the module's own path. The directory never
	// exists on disk — the overlay is the only place it does — and the name
	// is per run so two traces of one module cannot see each other's.
	rtName := "unfoldtrace_rt_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	rtImport := pkg.Module.Path + "/" + rtName
	overlay := map[string]string{}
	rtFile := filepath.Join(tmp, "rt.go")
	if err := os.WriteFile(rtFile, []byte(runtimeSource), 0o644); err != nil {
		return traces.Trace{}, err
	}
	overlay[filepath.Join(pkg.Module.Dir, rtName, "rt.go")] = rtFile

	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return traces.Trace{}, err
	}
	rewrite := func(files []string, pkgPath string) error {
		for _, base := range files {
			orig := filepath.Join(pkg.Dir, base)
			src, err := os.ReadFile(orig)
			if err != nil {
				return err
			}
			out, err := Rewrite(orig, src, pkgPath, rtImport)
			if err != nil {
				return fmt.Errorf("instrument %s: %w", orig, err)
			}
			dst := filepath.Join(srcDir, base)
			if err := os.WriteFile(dst, out, 0o644); err != nil {
				return err
			}
			overlay[orig] = dst
		}
		return nil
	}
	if err := rewrite(pkg.GoFiles, pkg.ImportPath); err != nil {
		return traces.Trace{}, err
	}
	if err := rewrite(pkg.TestGoFiles, pkg.ImportPath); err != nil {
		return traces.Trace{}, err
	}
	if err := rewrite(pkg.XTestGoFiles, pkg.ImportPath+"_test"); err != nil {
		return traces.Trace{}, err
	}

	ovPath := filepath.Join(tmp, "overlay.json")
	ovJSON, _ := json.Marshal(map[string]any{"Replace": overlay})
	if err := os.WriteFile(ovPath, ovJSON, 0o644); err != nil {
		return traces.Trace{}, err
	}

	eventsPath := filepath.Join(tmp, "events.log")
	// -count=1 because a cached test result is a test that did not run, and
	// -vet=off because vet cannot chdir into a directory that is only in
	// the overlay.
	args := []string{"test", "-count=1", "-vet=off", "-overlay", ovPath}
	if opts.Run != "" {
		args = append(args, "-run", opts.Run)
	}
	args = append(args, pkg.ImportPath)
	cmd := exec.Command("go", args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), "UNFOLD_TRACE="+eventsPath)
	if opts.MaxEvents > 0 {
		cmd.Env = append(cmd.Env, "UNFOLD_TRACE_MAX="+strconv.Itoa(opts.MaxEvents))
	}
	var testOut bytes.Buffer
	w := io.Writer(&testOut)
	if opts.Output != nil {
		w = io.MultiWriter(&testOut, opts.Output)
	}
	cmd.Stdout, cmd.Stderr = w, w
	runErr := cmd.Run()

	events, truncated, err := parseEvents(eventsPath)
	if err != nil {
		if runErr != nil {
			return traces.Trace{}, fmt.Errorf("go test failed: %v\n%s", runErr, testOut.String())
		}
		return traces.Trace{}, err
	}
	// A failing test is still a run worth replaying — often the one you
	// want — so the failure is reported but the trace is kept.
	if runErr != nil && opts.Output == nil {
		fmt.Fprintf(os.Stderr, "unfold trace: go test: %v\n", runErr)
	}

	command := "unfold trace"
	if opts.Run != "" {
		command += " -run " + opts.Run
	}
	command += " " + opts.Pattern
	return traces.Trace{
		Test:      testName(events, opts.Run),
		Package:   pkg.ImportPath,
		Command:   command,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Truncated: truncated,
		Events:    events,
	}, nil
}

type listedPackage struct {
	Dir          string
	ImportPath   string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
	Module       *struct {
		Path string
		Dir  string
	}
}

func listPackage(dir, pattern string) (*listedPackage, error) {
	if pattern == "" {
		pattern = "."
	}
	cmd := exec.Command("go", "list", "-json=Dir,ImportPath,GoFiles,TestGoFiles,XTestGoFiles,Module", pattern)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %v\n%s", pattern, err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []*listedPackage
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list %s: %v", pattern, err)
		}
		pkgs = append(pkgs, &p)
	}
	switch {
	case len(pkgs) == 0:
		return nil, fmt.Errorf("no package matches %q", pattern)
	case len(pkgs) > 1:
		return nil, fmt.Errorf("%q matches %d packages; trace one at a time", pattern, len(pkgs))
	}
	p := pkgs[0]
	if p.Module == nil {
		return nil, fmt.Errorf("%s is not in a module; tracing needs one", p.ImportPath)
	}
	if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
		return nil, fmt.Errorf("%s has no tests to run", p.ImportPath)
	}
	return p, nil
}

// parseEvents reads the runtime's log. Lines are "E g t line fn",
// "L g t line", "X g t", and a lone "T" when the cap was hit.
func parseEvents(path string) ([]traces.Event, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("no events were recorded (did the test run?): %v", err)
	}
	defer f.Close()
	var out []traces.Event
	truncated := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "T" {
			truncated = true
			continue
		}
		parts := strings.SplitN(line, " ", 5)
		if len(parts) < 3 {
			continue
		}
		g, _ := strconv.ParseInt(parts[1], 10, 64)
		t, _ := strconv.ParseInt(parts[2], 10, 64)
		ev := traces.Event{G: g, T: t}
		switch parts[0] {
		case "E":
			if len(parts) < 5 {
				continue
			}
			ev.Kind = "enter"
			ev.Line, _ = strconv.Atoi(parts[3])
			ev.Fn = parts[4]
		case "L":
			if len(parts) < 4 {
				continue
			}
			ev.Kind = "line"
			ev.Line, _ = strconv.Atoi(parts[3])
		case "X":
			ev.Kind = "exit"
		default:
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, fmt.Errorf("no events were recorded (did the test run?)")
	}
	return out, truncated, nil
}

// testName is the test the trace is of: the first Test function entered,
// which is the test when -run selected one, or else what -run said — a
// regexp, but the best name there is when no Test function was recorded.
func testName(events []traces.Event, run string) string {
	for _, ev := range events {
		if ev.Kind != "enter" {
			continue
		}
		base := ev.Fn[strings.LastIndex(ev.Fn, ".")+1:]
		if strings.HasPrefix(base, "Test") {
			return base
		}
	}
	return run
}
