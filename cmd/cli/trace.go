package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MaxInertia/unfold/internal/tracer"
	"github.com/MaxInertia/unfold/internal/traces"
)

// runTrace is the `unfold trace` subcommand: instrument one package, run a
// test in it, and write what executed to .unfold/traces/<name>.json for the
// replay panel. It is a subcommand rather than a flag of the server because
// it is a different kind of run — it builds and executes the user's code
// and then exits — and the browser must never be able to trigger that.
func runTrace(args []string) {
	fs := flag.NewFlagSet("unfold trace", flag.ExitOnError)
	var (
		run  = fs.String("run", "", "regexp passed to go test -run; empty runs every test in the package")
		name = fs.String("o", "", "trace name (the file is .unfold/traces/<name>.json); default: the test's name, or the package's")
		dir  = fs.String("dir", "", "project directory the trace is saved under and the package pattern is relative to (default: cwd)")
		max  = fs.Int("max-events", 0, "stop recording after this many events (default 200000)")
	)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: unfold trace [-run TestX] [-o name] [-dir DIR] [-max-events N] ./path/to/package\n\n")
		fmt.Fprintf(fs.Output(), "Runs the package's tests with every function instrumented and records the\nlines executed, in order, with the calls between them, for the replay panel.\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	pattern := fs.Arg(0)

	tr, err := tracer.Run(tracer.Options{
		Dir:       *dir,
		Pattern:   pattern,
		Run:       *run,
		MaxEvents: *max,
		Output:    os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unfold trace: %v\n", err)
		os.Exit(1)
	}
	tr.Name = *name
	if tr.Name == "" {
		tr.Name = tr.Test
	}
	if tr.Name == "" {
		tr.Name = tr.Package[strings.LastIndex(tr.Package, "/")+1:]
	}
	// Characters a -run regexp can carry that a filename should not.
	tr.Name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, tr.Name)
	tr.Name = strings.Trim(tr.Name, "._-")
	if tr.Name == "" {
		tr.Name = "trace"
	}

	path, err := traces.NewStore(*dir).Save(tr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unfold trace: %v\n", err)
		os.Exit(1)
	}
	sum := traces.Summarize(tr)
	note := ""
	if tr.Truncated {
		note = " (truncated at the event cap; raise it with -max-events)"
	}
	fmt.Printf("wrote %s: %d events on %d goroutine(s)%s\n", path, sum.Events, sum.Goroutines, note)
}
