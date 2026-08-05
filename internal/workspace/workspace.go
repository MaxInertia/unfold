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
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/manifest"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/protoapi"
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

	// servedBy maps a declared key ("<kind>\x00<key>") to the alias serving
	// it. This is the cross-repo join, and it needs no Go index at all.
	servedBy map[string]string
}

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
	if w.eager() {
		for _, alias := range w.order {
			_ = w.load(alias)
		}
	} else if err := w.load(w.primary); err != nil {
		// The repo the user actually opened must index, or there's nothing
		// to show; the rest may fail quietly until visited.
		return nil, err
	}
	return w, nil
}

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
// serialize on its lock rather than each paying the cost.
func (w *Workspace) load(alias string) error {
	r, ok := w.repos[alias]
	if !ok {
		return fmt.Errorf("unknown repository %q", alias)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return nil
	}
	if r.err != nil {
		return r.err // don't retry a repo that already failed to build
	}
	idx := indexer.New()
	_ = idx.SetProtoRoot(w.protoRoot)
	if err := idx.Load(r.dir, "./..."); err != nil {
		r.err = fmt.Errorf("%s: %w", r.name, err)
		return r.err
	}
	r.idx = idx
	r.loaded = true

	// A one-line summary per repo, because "0 outbound" is otherwise
	// indistinguishable from "recognized nothing" and there's no way to tell
	// from the UI which one you're looking at.
	if sv, err := idx.ServiceView(""); err == nil {
		fmt.Fprintf(os.Stderr, "unfold: indexed %s — %d inbound, %d outbound (%d declared rpc)\n",
			r.name, len(sv.Inbound), len(sv.Outbound), len(r.methods))
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
