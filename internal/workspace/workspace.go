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
	// loading is true while this repo's code is being read. Kept beside
	// `loaded` rather than derived from the load lock, because "is someone
	// holding the lock" is not a question a reader can ask without waiting for
	// the answer — which is the wait this whole split exists to avoid.
	loading bool
	err     error
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

	// channels maps a channel key ("<channel>\x00<key>") to the services on
	// each side of it — the cross-repo join. It fills from two sources:
	// declarations, which need no Go index at all and are read for the whole
	// workspace up front, and each repo's own bindings, which arrive as that
	// repo is indexed because nothing declares a subscription or a publish.
	//
	// Both sides, and a set on each, because a topic is not a gRPC method: it
	// can have several subscribers and several publishers, and a subscriber
	// wants to know who emits as much as a publisher wants to know who
	// listens. One repo per key answered exactly one of those questions and
	// silently dropped every end after the first.
	//
	// Guarded, unlike the rest of the workspace's immutable-after-Open state:
	// background loads run two at a time and each publishes what its repo is
	// an end of, while readers answer platform queries throughout.
	channelMu sync.RWMutex
	channels  map[string]*channelEnds

	// bg tracks the background eager load, so WaitIndexed can join it.
	bg sync.WaitGroup
}

// OnRepoChange is called whenever a repository starts or finishes indexing, so
// a UI can show what is happening without polling for it. Package-level for the
// same reason RulePaths is: it is a process-wide wiring, set once at startup.
//
// finished distinguishes the two edges, because they mean different things to
// a reader. A start changes only what the workspace is *doing*; a finish
// changes what it can *say* — a boundary drawn while a service was still being
// read reports no end at all, and would keep reporting that forever if nothing
// told the view to ask again.
//
// Called from whichever goroutine changed the state, including background
// loaders, so it must not block.
var OnRepoChange func(finished bool)

func repoChanged(finished bool) {
	if OnRepoChange != nil {
		OnRepoChange(finished)
	}
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
		// Naming the one-level rule here rather than just the directory: the
		// usual cause is a workspace whose checkouts sit a level deeper than
		// this looks, and "no Go modules found" reads like the repos are
		// missing rather than like they weren't looked for.
		return nil, fmt.Errorf("no go.mod in %s or any of its immediate subdirectories "+
			"(a workspace is the directory your repository checkouts sit directly in)", abs)
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
	return Reopen(nil, dirs, primaryDir, protoRoot, mode, nil)
}

// Reopen is Open, carrying over the indexes of repositories whose code cannot
// have changed since prev was built.
//
// A rebuild used to discard every index and read them all again, because it
// had no way to know why it was happening. Linking a repository made that
// obvious and expensive: adding one service re-read the four already open,
// which is minutes of work to learn something about none of them.
//
// rebuild names the repository directories that must be read again anyway —
// the one whose file was saved. A nil map keeps everything that exists; a
// rules change keeps nothing, because recognizers are a workspace-wide fact
// and a rule applied to some repos and not others is the silently-shrinking
// surface the whole rule system is careful about.
func Reopen(prev *Workspace, dirs []string, primaryDir, protoRoot string, mode Mode, rebuild map[string]bool) (*Workspace, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no repositories")
	}
	w := &Workspace{
		repos:     make(map[string]*repo, len(dirs)),
		mode:      mode,
		protoRoot: protoRoot,
		rulePaths: RulePaths,
		channels:  map[string]*channelEnds{},
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
	w.adopt(prev, rebuild)
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
	// Marked as soon as the workspace decides to read them, not when each
	// goroutine gets its turn. Only BackgroundLoaders run at once, so the rest
	// are waiting — and a repo that is going to be read reported itself as
	// idle, with a button offering to do the thing already scheduled. "Not
	// indexed" has to mean "not unless you ask", or it means nothing.
	marked := false
	for _, alias := range aliases {
		if w.repos[alias].setLoading(true) {
			marked = true
		}
	}
	if marked {
		repoChanged(false)
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
				// load clears the mark on every path it takes, but it returns
				// early for a repo already loaded or already failed — which is
				// reachable here, since being queued and being loaded by
				// someone else are not exclusive.
				if w.repos[a].setLoading(false) {
					repoChanged(true)
				}
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
			// A proto says this service *implements* the method: the inbound
			// end, known without reading any Go.
			w.publish("grpc.method", m.FullName, alias, model.RoleInbound)
		}
	}
}

// declKey is the join identity: what an outbound edge asks for, and what an
// inbound one answers with. It normalizes the kind, because the two ends of a
// channel are not called the same thing.
func declKey(kind, key string) string { return channelOf(kind) + "\x00" + key }

// channelOf collapses the names for the two ends of one channel to the channel
// itself.
//
// A gRPC method is called `grpc.method` from both sides — the caller names the
// method it calls and the server declares the method it implements — so the
// join could be an exact match on kind and nobody noticed. Pub/sub is not like
// that: the publish side is `pubsub.topic` and the subscribe side is
// `pubsub.subscription`, and rightly, because they are different roles. Joining
// on kind then meant the two ends of every pubsub edge could never meet,
// including for the built-in recognizers.
//
// The kind still namespaces the key everywhere it is *displayed*; this is only
// about what counts as the same channel when matching the ends.
func channelOf(kind string) string {
	switch kind {
	case "pubsub.topic", "pubsub.subscription":
		return "pubsub"
	}
	return kind
}

// Repos reports the workspace's repositories, for display.
func (w *Workspace) Repos() []model.RepoInfo {
	out := make([]model.RepoInfo, 0, len(w.order))
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		loaded, loading, err := r.loaded, r.loading, r.err
		r.mu.Unlock()
		info := model.RepoInfo{
			Alias:    alias,
			Name:     r.name,
			Dir:      r.dir,
			Primary:  alias == w.primary,
			Indexed:  loaded,
			Indexing: loading,
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
	if r.setLoading(true) {
		repoChanged(false)
	}
	defer func() {
		if r.setLoading(false) {
			repoChanged(true)
		}
	}()

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
		w.publishBindings(alias, sv)
	}
	return nil
}

// setLoading records whether this repo is being read or waiting to be read,
// reporting whether that changed. One state for both, because the reader's
// question is "is something going to happen without me asking" and the answer
// is yes either way — the difference between queued and running is unfold's
// business, not theirs.
func (r *repo) setLoading(v bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loading == v {
		return false
	}
	r.loading = v
	return true
}

// adopt takes over prev's indexes for repositories at the same directory,
// skipping any named in rebuild. An adopted repo republishes what it is an end
// of, because that is normally done by the load it just skipped.
func (w *Workspace) adopt(prev *Workspace, rebuild map[string]bool) {
	if prev == nil {
		return
	}
	byDir := make(map[string]*repo, len(prev.repos))
	for _, r := range prev.repos {
		byDir[r.dir] = r
	}
	for _, alias := range w.order {
		r := w.repos[alias]
		if rebuild[r.dir] {
			continue
		}
		old, ok := byDir[r.dir]
		if !ok {
			continue
		}
		old.mu.Lock()
		idx, loaded, err := old.idx, old.loaded, old.err
		old.mu.Unlock()
		if !loaded || idx == nil {
			// A repo that failed carries its failure over too: retrying it on
			// an unrelated rebuild would re-pay a compile that already failed,
			// every time anyone links anything.
			r.mu.Lock()
			r.err = err
			r.mu.Unlock()
			continue
		}
		r.mu.Lock()
		r.idx, r.loaded = idx, true
		r.mu.Unlock()
		if sv, err := idx.ServiceView(""); err == nil {
			w.publishBindings(alias, sv)
		}
	}
}

// channelEnds are the services on each side of one channel key.
type channelEnds struct {
	inbound  []string
	outbound []string
}

// publishBindings registers which sides of which channels this repo is on,
// according to its *code*.
//
// Declarations can't answer this. A proto file names the gRPC methods a
// service implements without indexing it, which is what makes the cross-repo
// gRPC join cheap — but nothing declares that a service subscribes to a topic,
// or that it publishes to one, so a pubsub edge can only come from the two
// bodies at its ends. That is why this half fills in as repos are indexed
// rather than up front: the alternative is indexing the whole workspace before
// showing anything, which is the startup cost that was deliberately removed.
//
// The consequence to keep in mind: an end in a service nobody has opened yet
// isn't known. In the case this exists for — both ends already indexed — it is
// known as soon as they are.
func (w *Workspace) publishBindings(alias string, sv *model.ServiceView) {
	for _, b := range sv.Inbound {
		w.publish(b.Kind, b.Key, alias, model.RoleInbound)
	}
	for _, b := range sv.Outbound {
		w.publish(b.Kind, b.Key, alias, model.RoleOutbound)
	}
}

// republishIndexed re-registers every already-indexed repo, for a rebuild of
// the join that would otherwise keep only the declared half.
func (w *Workspace) republishIndexed() {
	for _, alias := range w.order {
		r := w.repos[alias]
		r.mu.Lock()
		idx, loaded := r.idx, r.loaded
		r.mu.Unlock()
		if !loaded || idx == nil {
			continue
		}
		if sv, err := idx.ServiceView(""); err == nil {
			w.publishBindings(alias, sv)
		}
	}
}

// publish records that alias is on one side of a channel. Idempotent: a repo
// that emits to the same topic from five call sites is one publisher.
func (w *Workspace) publish(kind, key, alias string, role model.BindingRole) {
	if key == "" {
		return
	}
	w.channelMu.Lock()
	defer w.channelMu.Unlock()
	k := declKey(kind, key)
	ends := w.channels[k]
	if ends == nil {
		ends = &channelEnds{}
		w.channels[k] = ends
	}
	side := &ends.inbound
	if role == model.RoleOutbound {
		side = &ends.outbound
	}
	for _, a := range *side {
		if a == alias {
			return
		}
	}
	*side = append(*side, alias)
}

// endsOf reports the services on one side of a channel key, in a fixed order.
// Sorted rather than insertion-ordered: insertion order is whichever
// background load finished first, and a list that reshuffles between runs is
// one nobody can talk about.
func (w *Workspace) endsOf(kind, key string, role model.BindingRole) []string {
	w.channelMu.RLock()
	defer w.channelMu.RUnlock()
	ends := w.channels[declKey(kind, key)]
	if ends == nil {
		return nil
	}
	side := ends.inbound
	if role == model.RoleOutbound {
		side = ends.outbound
	}
	out := append([]string(nil), side...)
	sort.Strings(out)
	return out
}

// serverOf reports the first service on the inbound side of a key, for the
// surfaces with room to name one.
func (w *Workspace) serverOf(kind, key string) (string, bool) {
	ends := w.endsOf(kind, key, model.RoleInbound)
	if len(ends) == 0 {
		return "", false
	}
	return ends[0], true
}

// Channels lists every key the workspace has seen, with the services at each
// end. Read straight off the join — no Go index is touched, so this stays
// answerable for a whole workspace whatever has been opened.
//
// A key with an empty side is kept rather than filtered out. A topic nobody
// subscribes to and a subscription nobody publishes to are exactly what a
// reader is looking for when they open this, and dropping them would make the
// list agree with itself while disagreeing with the platform.
func (w *Workspace) Channels() []model.Channel {
	w.channelMu.RLock()
	keys := make([]string, 0, len(w.channels))
	sides := make(map[string]channelEnds, len(w.channels))
	for k, ends := range w.channels {
		keys = append(keys, k)
		sides[k] = channelEnds{
			inbound:  append([]string(nil), ends.inbound...),
			outbound: append([]string(nil), ends.outbound...),
		}
	}
	w.channelMu.RUnlock()

	sort.Strings(keys)
	out := make([]model.Channel, 0, len(keys))
	for _, k := range keys {
		channel, key, ok := splitDeclKey(k)
		if !ok {
			continue
		}
		ends := sides[k]
		out = append(out, model.Channel{
			Channel:  channel,
			Key:      key,
			Inbound:  w.channelEndsOf(ends.inbound),
			Outbound: w.channelEndsOf(ends.outbound),
		})
	}
	return out
}

// channelEndsOf names each service on one side, in a fixed order.
func (w *Workspace) channelEndsOf(aliases []string) []model.ChannelEnd {
	sorted := append([]string(nil), aliases...)
	sort.Strings(sorted)
	out := make([]model.ChannelEnd, 0, len(sorted))
	for _, alias := range sorted {
		r, ok := w.repos[alias]
		if !ok {
			continue
		}
		r.mu.Lock()
		loaded := r.loaded
		r.mu.Unlock()
		out = append(out, model.ChannelEnd{Repo: alias, Service: r.name, Indexed: loaded})
	}
	return out
}

// splitDeclKey undoes declKey, yielding the channel (the normalized kind) and
// the key. The separator is a NUL, which no kind or key can contain, so this
// can't be ambiguous.
func splitDeclKey(k string) (channel, key string, ok bool) {
	i := strings.IndexByte(k, 0)
	if i < 0 {
		return "", "", false
	}
	return k[:i], k[i+1:], true
}

// oppositeOf is the side to go looking for, given the side you are standing
// on: an emit leads to subscribers, a subscribe leads to publishers. An empty
// role means the caller didn't say — every link made before leaves carried one
// — and the old behaviour was to look inbound.
func oppositeOf(role model.BindingRole) model.BindingRole {
	if role == model.RoleInbound {
		return model.RoleOutbound
	}
	return model.RoleInbound
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
