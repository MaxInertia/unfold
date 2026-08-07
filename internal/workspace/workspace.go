// Package workspace federates several repositories behind one model.Engine,
// so an outbound call in one service can be followed into the implementation
// in another.
//
// Two layers, because they cost wildly different amounts:
//
//   - The *declaration* layer is manifests and protos. Reading it for every
//     repo costs milliseconds and no Go compilation, and it is already enough
//     to answer "which service serves this key" — that's the whole point of
//     the declared tier.
//   - The *code* layer is a full go/packages index per repo: seconds and
//     hundreds of megabytes each. It's needed only to render a frame.
//
// So the declaration layer is always built for the whole workspace, and the
// code layer is built per repo — eagerly for a small workspace, on demand for
// a large one.
//
// Only the primary repo's code layer is on the startup path. Everything else
// is indexed behind it, because startup would otherwise cost the sum of every
// repo in the workspace and none of it is needed to show the one you're
// standing in.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/manifest"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/protoapi"
	"github.com/MaxInertia/unfold/internal/rules"
)

// Sep separates a repo alias from an engine-specific id. Go's FullName uses
// "." "/" "(" "*" and the TS engine uses "#", so "::" collides with neither.
const Sep = "::"

// Mode selects when a repo's Go code is indexed.
type Mode string

const (
	ModeEager Mode = "eager"
	ModeLazy  Mode = "lazy"
	// ModeAuto indexes everything up front for a small workspace and defers
	// for a large one — the point where eager stops being a pause and starts
	// being a wait.
	ModeAuto Mode = "auto"
)

// EagerLimit is how many repos ModeAuto will index up front.
const EagerLimit = 4

type repo struct {
	alias string // stable key used in ids; the directory name
	name  string // display name (manifest's, falling back to alias)
	dir   string

	// Declaration layer, read at startup for every repo.
	mf      *manifest.Manifest
	methods []protoapi.Method

	// Code layer, possibly deferred.
	//
	// Two locks, because they are held for wildly different lengths of time.
	// loadMu serializes the indexing itself, so two callers who want the same
	// repo don't each pay for it; mu guards the three fields below and is held
	// for nanoseconds. Using one lock for both meant every reader — Repos(),
	// the platform view, a search — blocked for the *whole* multi-second load
	// of any repo being indexed in the background, which is precisely the wait
	// that moving it off the startup path was meant to remove.
	loadMu sync.Mutex
	mu     sync.Mutex
	idx    *indexer.Indexer
	loaded bool
	err    error
}

// Workspace implements model.Engine over several repos.
type Workspace struct {
	repos   map[string]*repo
	order   []string // aliases, sorted, for stable iteration
	primary string   // the repo unfold was pointed at; owns un-prefixed ids
	mode    Mode

	protoRoot string
	// rulePaths are the shared recognizer files, applied to every repo.
	rulePaths []string

	// servedBy maps a declared key ("<kind>\x00<key>") to the alias serving
	// it. This is the cross-repo join, and it needs no Go index at all.
	servedBy map[string]string

	// bg tracks the background eager load, so WaitIndexed can join it.
	bg sync.WaitGroup
}

// RulePaths are the shared recognizer files every repo in a workspace loads.
// Package-level to avoid threading it through Open's signature for what is a
// process-wide setting, the same way the engine treats the proto root.
var RulePaths []string

// Preload names repositories to index in the background whatever the mode
// says. Adding a repo by hand is a statement that you intend to go there —
// usually within seconds, by clicking the cross-repo jump that motivated
// linking it — so waiting for the index at that click is a wait the user
// already told us was coming.
//
// It matters most in the case that produced it: a lazy workspace, which is
// what an eager one becomes the moment linking pushes it past EagerLimit. One
// added repo would otherwise make the other four lazy as well.
var Preload []string

// Discover finds the repos under root: root itself if it's a module, plus
// every immediate subdirectory that is one. One level is deliberate — a
// workspace is a directory of checkouts, and walking deeper would index
// vendored copies and testdata modules.
func Discover(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var dirs []string
	if isModule(abs) {
		dirs = append(dirs, abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read workspace %s: %w", abs, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		d := filepath.Join(abs, e.Name())
		if isModule(d) {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no Go modules found in %s", abs)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// underDir reports whether path lies inside dir, comparing whole path
// segments so a sibling checkout like "orders-v2" isn't read as being inside
// "orders".
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isModule(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !fi.IsDir()
}

// Open builds a workspace over dirs. primaryDir is the repo the user pointed
// unfold at; its ids stay unprefixed so single-repo URLs and bookmarks keep
// working. protoRoot resolves manifests' protoPaths.
func Open(dirs []string, primaryDir, protoRoot string, mode Mode) (*Workspace, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no repositories")
	}
	w := &Workspace{
		repos:     make(map[string]*repo, len(dirs)),
		mode:      mode,
		protoRoot: protoRoot,
		rulePaths: RulePaths,
		servedBy:  map[string]string{},
	}
	primaryAbs, _ := filepath.Abs(primaryDir)
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		alias := uniqueAlias(w.repos, filepath.Base(abs))
		r := &repo{alias: alias, name: alias, dir: abs}
		w.repos[alias] = r
		w.order = append(w.order, alias)
		if abs == primaryAbs {
			w.primary = alias
		}
	}
	if w.primary == "" {
		// Launched inside a repo but below its root — a common way to run
		// this — so the repo containing the cwd is the one being read.
		for _, alias := range w.order {
			if underDir(primaryAbs, w.repos[alias].dir) {
				w.primary = alias
				break
			}
		}
	}
	if w.primary == "" {
		// Launched outside every repo (at the workspace root, typically).
		// Something has to be the service in view, but picking the
		// alphabetically first one silently is how you end up staring at an
		// empty surface wondering what broke.
		sort.Strings(w.order)
		w.primary = w.order[0]
		fmt.Fprintf(os.Stderr,
			"unfold: %s is not one of the workspace's repositories; showing %q. "+
				"Run from inside a repo, or pass --dir, to start there.\n",
			primaryAbs, w.primary)
	}
	sort.Strings(w.order)
	w.readDeclarations()
	// The repo the user actually opened must index, or there's nothing to
	// show; the rest may fail quietly until visited. Eager mode used to
	// swallow this one too, which meant a primary that didn't compile came up
	// as an empty workspace rather than as an error.
	if err := w.load(w.primary); err != nil {
		return nil, err
	}
	// Eager means "without being asked", not "before anything can be seen".
	// Indexing the rest inline made startup the *sum* of every repo in the
	// workspace, paid before the HTTP listener even opened — four large repos
	// is minutes of staring at a browser that hasn't been told to open yet.
	// None of it is needed to render the repo you're standing in.
	w.loadBehind(w.wanted())
	return w, nil
}

// wanted is which repos are indexed without being asked: all of them when the
// workspace is eager, and otherwise the ones explicitly linked.
func (w *Workspace) wanted() []string {
	var out []string
	eager := w.eager()
	preload := map[string]bool{}
	for _, d := range Preload {
		if abs, err := filepath.Abs(d); err == nil {
			preload[abs] = true
		}
	}
	for _, alias := range w.order {
		if alias == w.primary {
			continue // already indexed, synchronously
		}
		if eager || preload[w.repos[alias].dir] {
			out = append(out, alias)
		}
	}
	return out
}

// BackgroundLoaders is how many repos are indexed at once behind the primary.
// Deliberately not the core count: each go/packages load is already parallel
// internally and holds a whole type-checked module in memory — hundreds of
// megabytes for a large repo — so this trades wall-clock against a memory
// spike, and a workspace is opened on the same machine that has to run it.
const BackgroundLoaders = 2

// loadBehind indexes the named repos off the startup path.
//
// Failures are not fatal here and not retried: load records the error on the
// repo, Repos() reports it, and the platform view already has a place to say a
// service isn't indexed. Being told that in a UI you can see beats being told
// it on a terminal you've stopped watching.
func (w *Workspace) loadBehind(aliases []string) {
	if len(aliases) == 0 {
		return
	}
	w.bg.Add(1)
	go func() {
		defer w.bg.Done()
		sem := make(chan struct{}, BackgroundLoaders)
		var wg sync.WaitGroup
		for _, alias := range aliases {
			wg.Add(1)
			go func(a string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				_ = w.load(a)
			}(alias)
		}
		wg.Wait()
	}()
}

// WaitIndexed blocks until the background eager load has finished. Nothing in
// the serving path needs it — every read loads what it touches — but a test
// that asserts on the whole workspace does, and so would any future caller
// that wants the finished article rather than whatever is ready.
func (w *Workspace) WaitIndexed() { w.bg.Wait() }

func (w *Workspace) eager() bool {
	switch w.mode {
	case ModeEager:
		return true
	case ModeLazy:
		return false
	default:
		return len(w.order) <= EagerLimit
	}
}

func uniqueAlias(existing map[string]*repo, base string) string {
	alias := base
	for n := 2; ; n++ {
		if _, clash := existing[alias]; !clash {
			return alias
		}
		alias = fmt.Sprintf("%s-%d", base, n)
	}
}

// readDeclarations builds the cheap layer for every repo: manifest names and
// the gRPC surface each one declares. This is what resolves a cross-repo key
// without indexing anyone's Go code.
func (w *Workspace) readDeclarations() {
	for _, alias := range w.order {
		r := w.repos[alias]
		mf, err := manifest.Read(r.dir)
		if err != nil || mf == nil {
			continue
		}
		r.mf = mf
		if mf.Name != "" {
			r.name = mf.Name
		}
		paths, excluded := mf.IncludedProtos()
		if w.protoRoot == "" || len(paths) == 0 {
			continue
		}
		methods, err := protoapi.Load(w.protoRoot, paths, excluded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unfold: %s: %v\n", r.name, err)
		}
		r.methods = methods
		for _, m := range methods {
			if m.ExcludedFromSDK {
				continue // exists, but no other service can call it
			}
			w.servedBy[declKey("grpc.method", m.FullName)] = alias
		}
	}
}

func declKey(kind, key string) string { return kind + "\x00" + key }

// Repos reports the workspace's repositories, for display.
func (w *Workspace) Repos() []model.RepoInfo {
	out := make([]model.RepoInfo, 0, len(w.order))
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		loaded, err := r.loaded, r.err
		r.mu.Unlock()
		info := model.RepoInfo{
			Alias:   alias,
			Name:    r.name,
			Dir:     r.dir,
			Primary: alias == w.primary,
			Indexed: loaded,
		}
		if err != nil {
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	return out
}

// load indexes a repo's Go code, once. Concurrent callers for the same repo
// serialize on loadMu rather than each paying the cost — but the state lock is
// taken only to read the flags and to publish the result, so a reader is never
// held up by an index in progress.
func (w *Workspace) load(alias string) error {
	r, ok := w.repos[alias]
	if !ok {
		return fmt.Errorf("unknown repository %q", alias)
	}
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	r.mu.Lock()
	loaded, prevErr := r.loaded, r.err
	r.mu.Unlock()
	if loaded {
		return nil
	}
	if prevErr != nil {
		return prevErr // don't retry a repo that already failed to build
	}
	idx := indexer.New()
	// Rules are a platform-wide fact — they describe libraries, not one
	// service — so every repo in the workspace gets the same set, plus its own
	// .unfold/recognizers.json for local reality.
	idx.SetRules(rules.Load(append(append([]string{}, w.rulePaths...), rules.RepoPath(r.dir))...))
	_ = idx.SetProtoRoot(w.protoRoot)
	started := time.Now()
	if err := idx.Load(r.dir, "./..."); err != nil {
		wrapped := fmt.Errorf("%s: %w", r.name, err)
		r.mu.Lock()
		r.err = wrapped
		r.mu.Unlock()
		return wrapped
	}
	took := time.Since(started)
	r.mu.Lock()
	r.idx = idx
	r.loaded = true
	r.mu.Unlock()

	// A one-line summary per repo, because "0 outbound" is otherwise
	// indistinguishable from "recognized nothing" and there's no way to tell
	// from the UI which one you're looking at. The duration is there because
	// "why is this slow" is a question about one repo, not about the
	// workspace, and nothing else in the process can answer it.
	if sv, err := idx.ServiceView(""); err == nil {
		fmt.Fprintf(os.Stderr, "unfold: indexed %s in %s — %d inbound, %d outbound (%d declared rpc)\n",
			r.name, took.Round(time.Millisecond), len(sv.Inbound), len(sv.Outbound), len(r.methods))
	}
	return nil
}

// engineFor resolves a namespaced id to its repo's engine, loading it if
// needed, and returns the bare id.
func (w *Workspace) engineFor(id string) (*indexer.Indexer, string, error) {
	alias, bare := split(id)
	if alias == "" {
		alias = w.primary
	}
	r, ok := w.repos[alias]
	if !ok {
		return nil, "", fmt.Errorf("unknown repository %q", alias)
	}
	if err := w.load(alias); err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	idx := r.idx
	r.mu.Unlock()
	if idx == nil {
		return nil, "", fmt.Errorf("%s is not indexed", alias)
	}
	return idx, bare, nil
}

// split separates "alias::id". An id with no separator belongs to the
// primary repo, which is what keeps single-repo URLs and bookmarks valid.
func split(id string) (alias, bare string) {
	if a, b, ok := strings.Cut(id, Sep); ok {
		return a, b
	}
	return "", id
}

// qualify prefixes an id with its repo, except for the primary repo's, which
// stay bare.
func (w *Workspace) qualify(alias, id string) string {
	if id == "" || alias == w.primary {
		return id
	}
	return alias + Sep + id
}
