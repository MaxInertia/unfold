// Package tours reads and writes guided reading lists: an ordered set of
// places in the code, each with a description of why it matters, that the
// UI steps through. Tours live as JSON files under <project>/.unfold/tours/
// — one file per tour — for the same reason notes do: they are knowledge
// capture, so they should be plain text and committable. The sharper reason
// here is *who writes them*: a tour is usually authored by something other
// than the browser — an LLM working in the repo, or a colleague — and a file
// is the one interface every such author already has.
package tours

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Stop is one place to look. Exactly one of Symbol or File names where; the
// description says why. Expand lists calls to splice in when the stop
// opens, so a stop can be a pre-unfolded reading rather than a position:
// each entry is a call name as it appears at the call site ("retry.Do"),
// and " > " nests — "retry.Do > backoff.Next" opens backoff.Next inside
// retry.Do.
type Stop struct {
	Symbol      string   `json:"symbol,omitempty"`
	File        string   `json:"file,omitempty"`
	Line        int      `json:"line,omitempty"`
	Expand      []string `json:"expand,omitempty"`
	Description string   `json:"description"`
}

// Tour is one reading list. Name is the file's basename without .json and
// is the identity a client uses; it is not read from the file.
type Tour struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Stops       []Stop `json:"stops"`
	// UpdatedAt is the file's modification time, so a client can tell a
	// rewrite from a reload.
	UpdatedAt string `json:"updatedAt,omitempty"`
	// Error is set, with Stops empty, when the file could not be read as a
	// tour. The tour still lists, so a half-written or malformed file is
	// something the reader sees and can fix, not something that silently
	// isn't there.
	Error string `json:"error,omitempty"`
}

// tourFile is the on-disk shape: everything in Tour that is content rather
// than derived.
type tourFile struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Stops       []Stop `json:"stops"`
}

// Store reads and writes the tours directory for one project. It holds no
// cache: the directory is small, and reading it on every List is what makes
// a file written by someone else show up without any invalidation.
type Store struct {
	dir string // <project>/.unfold/tours
}

// NewStore addresses the tours directory for the project rooted at dir
// (empty means the working directory). Nothing is created until a Save.
func NewStore(dir string) *Store {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	return &Store{dir: filepath.Join(dir, ".unfold", "tours")}
}

// Dir is the directory tours are read from — what to tell an author.
func (s *Store) Dir() string { return s.dir }

// List reads every *.json in the tours directory, sorted by name. A missing
// directory is an empty list; a file that is not a tour is listed with its
// Error set.
func (s *Store) List() []Tour {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Tour
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, s.read(e.Name()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get reads one tour by name.
func (s *Store) Get(name string) (Tour, error) {
	if err := validateName(name); err != nil {
		return Tour{}, err
	}
	if _, err := os.Stat(filepath.Join(s.dir, name+".json")); err != nil {
		return Tour{}, fmt.Errorf("no tour named %q", name)
	}
	return s.read(name + ".json"), nil
}

func (s *Store) read(base string) Tour {
	t := Tour{Name: strings.TrimSuffix(base, ".json")}
	path := filepath.Join(s.dir, base)
	if fi, err := os.Stat(path); err == nil {
		t.UpdatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Error = err.Error()
		return t
	}
	var f tourFile
	if err := json.Unmarshal(buf, &f); err != nil {
		t.Error = "not valid JSON: " + err.Error()
		return t
	}
	t.Title, t.Description, t.Stops = f.Title, f.Description, f.Stops
	if err := Validate(t); err != nil {
		t.Error = err.Error()
		t.Stops = nil
	}
	return t
}

// Save writes a tour to <name>.json, replacing any existing one. The name
// must be a plain filename stem, since it becomes a path.
func (s *Store) Save(t Tour) (Tour, error) {
	if err := validateName(t.Name); err != nil {
		return Tour{}, err
	}
	if err := Validate(t); err != nil {
		return Tour{}, err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Tour{}, err
	}
	buf, err := json.MarshalIndent(tourFile{Title: t.Title, Description: t.Description, Stops: t.Stops}, "", "  ")
	if err != nil {
		return Tour{}, err
	}
	path := filepath.Join(s.dir, t.Name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return Tour{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return Tour{}, err
	}
	return s.read(t.Name + ".json"), nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateName(name string) error {
	if !namePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("tour name %q: use letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

// Validate checks that every stop says where to look. Descriptions are not
// required — a bare list of places is still a tour — but a stop with
// neither a symbol nor a file points nowhere.
func Validate(t Tour) error {
	if len(t.Stops) == 0 {
		return errors.New("a tour needs at least one stop")
	}
	for i, st := range t.Stops {
		switch {
		case st.Symbol != "" && st.File != "":
			return fmt.Errorf("stop %d: give a symbol or a file, not both", i+1)
		case st.Symbol == "" && st.File == "":
			return fmt.Errorf("stop %d: needs a symbol or a file", i+1)
		case st.Line < 0:
			return fmt.Errorf("stop %d: line must be 1-based", i+1)
		case st.Line > 0 && st.File == "":
			return fmt.Errorf("stop %d: line goes with a file, not a symbol", i+1)
		}
		for _, e := range st.Expand {
			if strings.TrimSpace(e) == "" {
				return fmt.Errorf("stop %d: empty expand entry", i+1)
			}
		}
	}
	return nil
}

// Watch calls onChange (debounced) whenever a tour file is written, created,
// renamed or removed. The engine's source watcher skips hidden directories,
// and .unfold is one, so this is its own watcher. The tours directory need
// not exist yet: the nearest existing ancestor is watched, and each level is
// picked up as it is created, so a tour written into a fresh project shows
// up without a restart.
func (s *Store) Watch(onChange func()) (io.Closer, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &watcher{fw: fw, dir: s.dir, onChange: onChange, done: make(chan struct{})}
	// The chain of directories from the project root down to tours/; a
	// watch sits on each one that exists.
	unfoldDir := filepath.Dir(s.dir)
	w.levels = []string{filepath.Dir(unfoldDir), unfoldDir, s.dir}
	w.addExisting()
	go w.loop()
	return w, nil
}

type watcher struct {
	fw       *fsnotify.Watcher
	dir      string
	levels   []string
	onChange func()
	done     chan struct{}
	once     sync.Once
}

func (w *watcher) Close() error {
	w.once.Do(func() { close(w.done) })
	return w.fw.Close()
}

func (w *watcher) addExisting() {
	for _, d := range w.levels {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return
		}
		if err := w.fw.Add(d); err != nil {
			log.Printf("tours: cannot watch %s: %v", d, err)
			return
		}
	}
}

func (w *watcher) isLevel(path string) bool {
	clean := filepath.Clean(path)
	for _, d := range w.levels[1:] {
		if clean == d {
			return true
		}
	}
	return false
}

func (w *watcher) loop() {
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	schedule := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(150*time.Millisecond, func() {
			select {
			case fire <- struct{}{}:
			default:
			}
		})
	}
	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			// .unfold or .unfold/tours appeared: watch it, and whatever is
			// already inside it. Its contents may have been written before
			// the watch landed, so that counts as a change too.
			if ev.Op&fsnotify.Create != 0 && w.isLevel(ev.Name) {
				w.addExisting()
				schedule()
				continue
			}
			if filepath.Dir(ev.Name) != w.dir || !strings.HasSuffix(ev.Name, ".json") {
				continue
			}
			schedule()
		case <-fire:
			select {
			case <-w.done:
				return
			default:
			}
			w.onChange()
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("tours watch error: %v", err)
		}
	}
}
