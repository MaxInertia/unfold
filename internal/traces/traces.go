// Package traces reads and writes execution traces: the ordered list of
// enter / line / exit events one run of a test produced, which the UI
// replays through the unfolded code. Traces live as JSON files under
// <project>/.unfold/traces/ — one per recorded run — next to tours and
// notes, and for the same reason: they are produced by a command (`unfold
// trace`) rather than the browser, and a file is the interface the command
// and the server already share.
package traces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Event is one thing the instrumented program did. Kind is "enter" (a
// function began; Fn names it and Line is its declaration line), "line" (a
// statement on Line began) or "exit" (the function on top of the
// goroutine's stack returned). G is the goroutine, so one goroutine's
// events can be replayed as a stack; T is nanoseconds since the run's
// first event.
type Event struct {
	G    int64  `json:"g"`
	Kind string `json:"kind"`
	Fn   string `json:"fn,omitempty"`
	Line int    `json:"line,omitempty"`
	T    int64  `json:"t"`
}

// Trace is one recorded run. Name is the file's basename without .json and
// is the identity a client uses; it is not read from the file.
type Trace struct {
	Name    string `json:"name"`
	Test    string `json:"test,omitempty"`
	Package string `json:"package"`
	// Command is how the trace was produced, so a stale one can be redone.
	Command   string  `json:"command,omitempty"`
	CreatedAt string  `json:"createdAt,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Events    []Event `json:"events"`
	// UpdatedAt is the file's modification time; Error is set, with Events
	// empty, when the file could not be read as a trace. The trace still
	// lists so a malformed file is seen rather than silently absent.
	UpdatedAt string `json:"updatedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Summary is what a list shows: everything but the events, plus their
// count. A trace can run to hundreds of thousands of events, so the list
// endpoint does not ship them.
type Summary struct {
	Name       string `json:"name"`
	Test       string `json:"test,omitempty"`
	Package    string `json:"package"`
	Command    string `json:"command,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Events     int    `json:"events"`
	Goroutines int    `json:"goroutines"`
	Error      string `json:"error,omitempty"`
}

// traceFile is the on-disk shape: everything in Trace that is content
// rather than derived.
type traceFile struct {
	Test      string  `json:"test,omitempty"`
	Package   string  `json:"package"`
	Command   string  `json:"command,omitempty"`
	CreatedAt string  `json:"createdAt,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Events    []Event `json:"events"`
}

// Store addresses the traces directory for one project. No cache: the
// directory is read on every List, which is what makes a trace written by
// the CLI show up without invalidation.
type Store struct {
	dir string // <project>/.unfold/traces
}

// NewStore addresses the traces directory for the project rooted at dir
// (empty means the working directory). Nothing is created until a Save.
func NewStore(dir string) *Store {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	return &Store{dir: filepath.Join(dir, ".unfold", "traces")}
}

// Dir is the directory traces are read from — what to tell a user.
func (s *Store) Dir() string { return s.dir }

// List summarizes every *.json in the traces directory, sorted by name. A
// missing directory is an empty list.
func (s *Store) List() []Summary {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, Summarize(s.read(e.Name())))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Summarize reduces a trace to its listing.
func Summarize(t Trace) Summary {
	gs := map[int64]struct{}{}
	for _, ev := range t.Events {
		gs[ev.G] = struct{}{}
	}
	return Summary{
		Name:       t.Name,
		Test:       t.Test,
		Package:    t.Package,
		Command:    t.Command,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
		Truncated:  t.Truncated,
		Events:     len(t.Events),
		Goroutines: len(gs),
		Error:      t.Error,
	}
}

// Get reads one trace by name.
func (s *Store) Get(name string) (Trace, error) {
	if err := validateName(name); err != nil {
		return Trace{}, err
	}
	if _, err := os.Stat(filepath.Join(s.dir, name+".json")); err != nil {
		return Trace{}, fmt.Errorf("no trace named %q", name)
	}
	return s.read(name + ".json"), nil
}

func (s *Store) read(base string) Trace {
	t := Trace{Name: strings.TrimSuffix(base, ".json")}
	path := filepath.Join(s.dir, base)
	if fi, err := os.Stat(path); err == nil {
		t.UpdatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Error = err.Error()
		return t
	}
	var f traceFile
	if err := json.Unmarshal(buf, &f); err != nil {
		t.Error = "not valid JSON: " + err.Error()
		return t
	}
	t.Test, t.Package, t.Command, t.CreatedAt, t.Truncated, t.Events =
		f.Test, f.Package, f.Command, f.CreatedAt, f.Truncated, f.Events
	if err := Validate(t); err != nil {
		t.Error = err.Error()
		t.Events = nil
	}
	return t
}

// Save writes a trace to <name>.json, replacing any existing one, and
// returns the path written. The name must be a plain filename stem, since
// it becomes a path.
func (s *Store) Save(t Trace) (string, error) {
	if err := validateName(t.Name); err != nil {
		return "", err
	}
	if err := Validate(t); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	f := traceFile{Test: t.Test, Package: t.Package, Command: t.Command, CreatedAt: t.CreatedAt, Truncated: t.Truncated, Events: t.Events}
	if f.Events == nil {
		f.Events = []Event{}
	}
	// Compact: a trace is written by a program and read by one, and at
	// hundreds of thousands of events indentation would double the file.
	buf, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, t.Name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateName(name string) error {
	if !namePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("trace name %q: use letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

// Validate checks the events are the shape a replay can walk: known kinds,
// a function name on every enter, and a statement line on every line event.
// It does not check that enters and exits pair up — a truncated trace, or a
// goroutine still running when the process ended, legitimately leaves
// functions open.
func Validate(t Trace) error {
	if t.Package == "" {
		return errors.New("a trace needs a package")
	}
	for i, ev := range t.Events {
		switch ev.Kind {
		case "enter":
			if ev.Fn == "" {
				return fmt.Errorf("event %d: enter without a function name", i)
			}
		case "line":
			if ev.Line <= 0 {
				return fmt.Errorf("event %d: line event needs a 1-based line", i)
			}
		case "exit":
		default:
			return fmt.Errorf("event %d: unknown kind %q", i, ev.Kind)
		}
	}
	return nil
}
