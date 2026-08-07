package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/engine"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/notes"
	"github.com/MaxInertia/unfold/internal/prefs"
	"github.com/MaxInertia/unfold/internal/rules"
)

//go:embed all:static/dist
var staticFS embed.FS

type Server struct {
	engine model.Engine
	static fs.FS
	target string
	differ *diff.Differ // nil = diff mode off
	notes  *notes.Store // nil = notes disabled
	// projectDir is where per-project prefs are persisted (the proto root a
	// user picks in the UI). Empty disables persistence.
	projectDir string
	// reload rebuilds the engine in place. Nil disables the endpoints that
	// need one, rather than letting them half-apply a change.
	reload func() error

	// Connected /api/events subscribers, notified when the engine reindexes.
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

// New builds a server backed by any indexing engine (Go or TypeScript).
func New(engine model.Engine) *Server {
	sub, err := fs.Sub(staticFS, "static/dist")
	if err != nil {
		panic(err)
	}
	return &Server{engine: engine, static: sub, clients: map[chan struct{}]struct{}{}}
}

// SetTarget records the indexer pattern (e.g. "./...") for the /api/health response.
func (s *Server) SetTarget(target string) { s.target = target }

// SetDiffer enables diff annotations on returned frames, comparing against the
// base engine d wraps. Nil leaves diff mode off.
func (s *Server) SetDiffer(d *diff.Differ) { s.differ = d }

// SetReloader supplies the function that rebuilds the engine, enabling the
// endpoints that change what the engine is built from.
func (s *Server) SetReloader(fn func() error) { s.reload = fn }

// SetNotes enables the notes API backed by the given store.
func (s *Server) SetNotes(n *notes.Store) { s.notes = n }

// SetProjectDir records where to persist choices made in the UI, so a proto
// root picked once survives a restart.
func (s *Server) SetProjectDir(dir string) { s.projectDir = dir }

// writeFrame attaches diff info (when diff mode is on) and writes the frame.
func (s *Server) writeFrame(w http.ResponseWriter, frame *model.Frame) {
	if s.differ != nil {
		s.differ.Annotate(frame)
	}
	writeJSON(w, http.StatusOK, frame)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/symbol", s.handleSymbol)
	mux.HandleFunc("/api/body", s.handleBody)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/typeinfo", s.handleTypeInfo)
	mux.HandleFunc("/api/usages", s.handleUsages)
	mux.HandleFunc("/api/service", s.handleService)
	mux.HandleFunc("/api/proto-root", s.handleProtoRoot)
	mux.HandleFunc("/api/repos", s.handleRepos)
	mux.HandleFunc("/api/rules", s.handleRules)
	mux.HandleFunc("/api/dirs", s.handleDirs)
	mux.HandleFunc("/api/resolve", s.handleResolve)
	mux.HandleFunc("/api/platform", s.handlePlatform)
	mux.HandleFunc("/api/index-repo", s.handleIndexRepo)
	mux.HandleFunc("/api/notes", s.handleNotes)
	mux.HandleFunc("/api/open", s.handleOpen)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.Handle("/", http.FileServer(http.FS(s.static)))
	return mux
}

// handleEvents is a Server-Sent Events stream. It emits a "reload" event each
// time the engine reindexes (see NotifyReload), plus periodic comment pings to
// keep proxies from closing an idle connection. The frontend refetches the
// current view when it receives a reload.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan struct{}, 1)
	s.addClient(ch)
	defer s.removeClient(ch)

	// Open the stream so EventSource fires onopen immediately.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: reload\ndata: {}\n\n")
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// NotifyReload wakes every connected /api/events subscriber. Non-blocking: a
// client that hasn't drained its previous notification already has a reload
// pending, so dropping the duplicate is fine.
func (s *Server) NotifyReload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) addClient(ch chan struct{}) {
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(ch chan struct{}) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
}

// GET /api/typeinfo?targetId=<id>&offset=<utf16-offset-in-source>
func (s *Server) handleTypeInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("targetId")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: targetId")
		return
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	ti, err := s.engine.TypeInfo(model.TargetID(id), offset)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// ti may be nil (offset not over a symbol) — that's a valid empty result.
	writeJSON(w, http.StatusOK, map[string]any{"typeInfo": ti})
}

// GET /api/usages?targetId=<id> — the places the target is referenced
// (callers, interface dispatches that may reach it, value references).
func (s *Server) handleUsages(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("targetId")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: targetId")
		return
	}
	usages, err := s.engine.Usages(model.TargetID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if usages == nil {
		usages = []model.Usage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usages": usages})
}

// GET /api/service[?anchor=<targetId>] — the service-level (zoomed-out) view:
// what enters this service and what it reaches out to.
//
// The optional anchor is the frame the user zoomed out from; bindings whose
// handler transitively reaches it come back flagged, which is what lets the
// view highlight the two routes that concern you out of eighty.
func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	pe, ok := s.engine.(model.PlatformEngine)
	if !ok {
		writeError(w, http.StatusNotImplemented, "platform view is not available for this engine")
		return
	}
	anchor := model.TargetID(r.URL.Query().Get("anchor"))
	// ?repo= selects which workspace service the view is about; without it
	// the view is about the repo unfold was pointed at.
	var (
		view *model.ServiceView
		err  error
	)
	if repo := r.URL.Query().Get("repo"); repo != "" {
		we, ok := s.engine.(model.WorkspaceEngine)
		if !ok {
			writeError(w, http.StatusNotImplemented, model.ErrNoWorkspace.Error())
			return
		}
		view, err = we.ServiceViewOf(repo, anchor)
	} else {
		view, err = pe.ServiceView(anchor)
	}
	if err != nil {
		if errors.Is(err, model.ErrNoPlatformView) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// GET  /api/rules — every recognizer, built-in and configured, with whether
// it's on, where it came from, and how many bindings it actually produced.
// POST /api/rules {"rule": {...}} — save a rule to the project's own file and
// rebuild; {"id": "...", "enabled": false} switches one off, built-ins
// included.
//
// A rule changes what the index contains, so applying one is a rebuild — the
// same path linking a repo takes, and the same reason: what a rule matched is
// only knowable by running it. That's also why the match count comes back
// *after* saving rather than as a preview; a dry run would cost a full reindex
// to answer the same question.
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	reporter, canReport := s.engine.(interface{ RuleReport() model.RuleReport })

	if r.Method == http.MethodGet {
		if !canReport {
			writeJSON(w, http.StatusOK, model.RuleReport{Rules: []model.RuleInfo{}})
			return
		}
		writeJSON(w, http.StatusOK, reporter.RuleReport())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if s.reload == nil || s.projectDir == "" {
		writeError(w, http.StatusNotImplemented, "this session can't save rules")
		return
	}

	var body struct {
		Rule   json.RawMessage `json:"rule"`
		Delete string          `json:"delete,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	path := rules.RepoPath(s.projectDir)
	existing := readRuleFile(path)

	if body.Delete != "" {
		kept := existing.Rules[:0]
		for _, r := range existing.Rules {
			if r.ID != body.Delete {
				kept = append(kept, r)
			}
		}
		existing.Rules = kept
	} else {
		var incoming rules.Rule
		if err := json.Unmarshal(body.Rule, &incoming); err != nil {
			writeError(w, http.StatusBadRequest, "invalid rule: "+err.Error())
			return
		}
		// Validate before writing. A rule that can never fire, or one that
		// would match every call site, is refused here rather than saved and
		// then quietly doing nothing — silence is the failure mode this whole
		// system exists to avoid.
		if _, problems, err := rules.Parse(mustJSON(rules.File{Rules: []rules.Rule{incoming}})); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		} else if len(problems) > 0 {
			writeError(w, http.StatusUnprocessableEntity, problems[0].Error())
			return
		}
		replaced := false
		for i := range existing.Rules {
			if existing.Rules[i].ID == incoming.ID {
				existing.Rules[i] = incoming
				replaced = true
				break
			}
		}
		if !replaced {
			existing.Rules = append(existing.Rules, incoming)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(path, mustJSON(existing), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.reload(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "rules saved but the index failed to rebuild: "+err.Error())
		return
	}
	s.NotifyReload()
	if reporter, ok := s.engine.(interface{ RuleReport() model.RuleReport }); ok {
		writeJSON(w, http.StatusOK, reporter.RuleReport())
		return
	}
	writeJSON(w, http.StatusOK, model.RuleReport{})
}

func readRuleFile(path string) rules.File {
	data, err := os.ReadFile(path)
	if err != nil {
		return rules.File{}
	}
	var f rules.File
	_ = json.Unmarshal(data, &f)
	return f
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return append(b, '\n')
}

// POST /api/repos {"path": "<abs dir>"[, "unlink": true]} — open another
// repository, or stop opening one, without restarting.
//
// You find out mid-session that the call you're following lands in a repo you
// didn't open, and until now the only answer was to quit and relaunch with
// --workspace pointed somewhere that happened to contain both. A linked repo
// need not be a sibling of anything.
//
// Linking rebuilds the engine rather than mutating the open workspace. The
// workspace's repo set, alias table and cross-repo declaration join are read
// without locks by every request path, on the assumption that they're fixed
// after Open — mutating them live would mean auditing all of it for races,
// where a rebuild is the mechanism watch mode already uses: it swaps
// atomically and keeps the previous engine if the new one fails to build. The
// cost is re-indexing what was eagerly loaded, which is why a large workspace
// defers to --index lazy anyway.
//
// Mutating and filesystem-touching, so it's guarded like /api/open: POST only
// and same-origin only.
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if s.reload == nil {
		writeError(w, http.StatusNotImplemented, "this session can't rebuild its index")
		return
	}
	var body struct {
		Path   string `json:"path"`
		Unlink bool   `json:"unlink"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	abs, err := filepath.Abs(expandHome(strings.TrimSpace(body.Path)))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if body.Unlink {
		if !engine.UnlinkRepo(abs) {
			writeError(w, http.StatusBadRequest, abs+" is not a linked repository")
			return
		}
	} else {
		// Reject a non-module up front. Discovering it after the rebuild would
		// mean reporting the failure against an engine that had already been
		// replaced, and the user would have paid the reindex for nothing.
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			writeError(w, http.StatusBadRequest, "not a directory: "+abs)
			return
		}
		if fi, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil || fi.IsDir() {
			writeError(w, http.StatusBadRequest, "not a Go module (no go.mod): "+abs)
			return
		}
		if !engine.LinkRepo(abs) {
			writeError(w, http.StatusConflict, abs+" is already open")
			return
		}
	}

	if err := s.reload(); err != nil {
		// The engine is unchanged — Reload keeps the previous one on failure —
		// so the link has to be rolled back too, or the next rebuild for any
		// reason would silently apply a repo the user was told had failed.
		if body.Unlink {
			engine.LinkRepo(abs)
		} else {
			engine.UnlinkRepo(abs)
		}
		writeError(w, http.StatusUnprocessableEntity, "could not open "+abs+": "+err.Error())
		return
	}

	if s.projectDir != "" {
		p := prefs.Load(s.projectDir)
		p.LinkedRepos = append([]string(nil), engine.LinkedRepos...)
		if err := prefs.Save(s.projectDir, p); err != nil {
			log.Printf("unfold: could not persist linked repositories: %v", err)
		}
	}
	s.NotifyReload()

	// Unlinking the last repo leaves a plain single-repo engine, whose Repos
	// is nil — which would marshal to null where the client expects a list.
	repos := []model.RepoInfo{}
	if lister, ok := s.engine.(interface{ Repos() []model.RepoInfo }); ok {
		if got := lister.Repos(); got != nil {
			repos = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos, "linked": engine.LinkedRepos})
}

// POST /api/proto-root {"path": "<abs dir>"} — point the declared gRPC
// surface at the shared proto repository.
//
// The paths in a microservice.yaml are relative to that repo, so unfold can't
// find it on its own, and a browser can't hand back a real filesystem path
// from a native directory picker. So the UI browses via /api/dirs and posts
// the chosen path here.
//
// Mutating and filesystem-touching, so it's guarded like /api/open: POST only
// and same-origin only.
func (s *Server) handleProtoRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	setter, ok := s.engine.(interface{ SetProtoRoot(string) error })
	if !ok {
		writeError(w, http.StatusNotImplemented, "this engine has no declared proto surface")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	dir := strings.TrimSpace(body.Path)
	if dir != "" {
		abs, err := filepath.Abs(expandHome(dir))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			writeError(w, http.StatusBadRequest, "not a directory: "+abs)
			return
		}
		dir = abs
	}

	// A root that doesn't resolve the declared protos is a user-visible
	// failure, not a server error: report it and let them pick again. The
	// engine has already applied it either way, so the view that comes back
	// reflects what's actually configured.
	setErr := setter.SetProtoRoot(dir)
	if dir != "" && s.projectDir != "" {
		p := prefs.Load(s.projectDir)
		p.ProtoRoot = dir
		if err := prefs.Save(s.projectDir, p); err != nil {
			log.Printf("unfold: could not persist proto root: %v", err)
		}
	}
	if setErr != nil {
		writeError(w, http.StatusUnprocessableEntity, setErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"protoRoot": dir})
}

// GET /api/resolve?kind=<binding kind>&key=<join key> — open the
// implementation of an outbound edge in whichever workspace repo serves it.
//
// Split from /api/service because the two cost different amounts: the service
// view answers "who serves this" from declarations alone, instantly, while
// this is where a lazily-indexed repo's Go code actually gets built. Keeping
// them apart is what lets a large workspace stay responsive until you commit
// to the jump.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	cr, ok := s.engine.(model.CrossRepoResolver)
	if !ok {
		writeError(w, http.StatusNotImplemented, model.ErrNoWorkspace.Error())
		return
	}
	q := r.URL.Query()
	kind, key := q.Get("kind"), q.Get("key")
	if kind == "" || key == "" {
		writeError(w, http.StatusBadRequest, "missing required query params: kind, key")
		return
	}
	res, err := cr.Resolve(kind, key)
	if err != nil {
		if errors.Is(err, model.ErrNoWorkspace) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/platform — every service in the workspace and the calls between
// them. Available only with a workspace open; a single repo has a service
// view but nothing above it.
func (s *Server) handlePlatform(w http.ResponseWriter, r *http.Request) {
	we, ok := s.engine.(model.WorkspaceEngine)
	if !ok {
		writeError(w, http.StatusNotImplemented, model.ErrNoWorkspace.Error())
		return
	}
	pv, err := we.PlatformView(model.TargetID(r.URL.Query().Get("anchor")))
	if err != nil {
		if errors.Is(err, model.ErrNoWorkspace) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

// POST /api/index-repo {"alias": "<service>"} — index one service's code.
//
// The platform view lists every service from declarations but can only draw
// *outgoing* edges for services it has read, so this fills the picture in one
// service at a time rather than making the user index the whole workspace.
// It's POST because it's the expensive, state-changing half.
func (s *Server) handleIndexRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	indexer, ok := s.engine.(interface{ IndexRepo(string) error })
	if !ok {
		writeError(w, http.StatusNotImplemented, model.ErrNoWorkspace.Error())
		return
	}
	var body struct {
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.Alias == "" {
		writeError(w, http.StatusBadRequest, "missing required field: alias")
		return
	}
	if err := indexer.IndexRepo(body.Alias); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/dirs?path=<dir> — the subdirectories of path, so the UI can offer
// a directory picker. With no path, it starts at the user's home directory.
//
// This lists directories outside the indexed project by design: the proto
// repository is a *different* repo, so the containment check that guards
// /api/open can't apply here. The same-origin guard is what keeps another
// page in the browser from walking the filesystem, and unfold binds to
// localhost. Only directory names are returned — never file contents.
func (s *Server) handleDirs(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)
	resp := map[string]any{"path": abs, "dirs": dirs}
	if parent := filepath.Dir(abs); parent != abs {
		resp["parent"] = parent
	}
	writeJSON(w, http.StatusOK, resp)
}

// expandHome resolves a leading ~ so a typed path behaves like it would in a
// shell.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// /api/notes — list (GET), upsert (POST a Note; empty id creates), delete
// (DELETE ?id=). Mutations are same-origin-guarded like /api/open: notes
// write a file under the project root, so a cross-origin page must not be
// able to trigger them.
func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil {
		writeError(w, http.StatusNotImplemented, "notes are not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"notes": s.notes.List()})
	case http.MethodPost:
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		var n notes.Note
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&n); err != nil {
			writeError(w, http.StatusBadRequest, "invalid note: "+err.Error())
			return
		}
		saved, err := s.notes.Upsert(n)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing required query param: id")
			return
		}
		if err := s.notes.Delete(id); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "use GET, POST, or DELETE")
	}
}

// POST /api/open?file=<abs-path>&line=<n> — opens the file in the configured
// editor. The command comes from $UNFOLD_EDITOR (a template with {file} and
// {line}); it defaults to VS Code's "code -g {file}:{line}".
//
// This is the one side-effecting endpoint, so it's guarded three ways: it
// requires POST (so a cross-origin <img>/<form> GET can't trigger it), it
// rejects cross-site requests via Fetch-Metadata / Origin, and it only opens
// files that are part of the indexed project — never an arbitrary host path.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	q := r.URL.Query()
	file := q.Get("file")
	if file == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: file")
		return
	}
	resolved, ok := s.resolveIndexedFile(file)
	if !ok {
		writeError(w, http.StatusForbidden, "file is not part of the indexed project")
		return
	}
	if err := openInEditor(resolved, q.Get("line")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// resolveIndexedFile reports whether file resolves to one of the project's
// indexed source files and, if so, returns that file's canonical indexed
// path. This is the containment check that keeps /api/open from opening (and
// thereby exfiltrating into the editor) arbitrary host files.
//
// The caller hands the *returned* path — not its own query string — to the
// editor, so what gets opened is exactly the file that passed containment,
// not whatever un-normalized form (e.g. with ../ segments) reached the API.
func (s *Server) resolveIndexedFile(file string) (string, bool) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	for _, f := range s.engine.Files() {
		if c := filepath.Clean(f); c == abs {
			return c, true
		}
	}
	return "", false
}

// sameOrigin rejects cross-origin browser requests. It prefers the Fetch
// Metadata header (sent by modern browsers) and falls back to comparing the
// Origin host with the request host. A request with neither header (curl, a
// same-origin navigation) is allowed.
//
// Note same-site is rejected, not just cross-site: the threat here is another
// app on the same machine (a different localhost:port is same-site), so only a
// genuinely same-origin request — or a non-browser client — may open a file.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func openInEditor(file, line string) error {
	if line == "" {
		line = "1"
	}
	if fi, err := os.Stat(file); err != nil || fi.IsDir() {
		return fmt.Errorf("not a readable file: %s", file)
	}
	tmpl := os.Getenv("UNFOLD_EDITOR")
	if tmpl == "" {
		tmpl = "code -g {file}:{line}"
	}
	parts := strings.Fields(tmpl)
	if len(parts) == 0 {
		return fmt.Errorf("UNFOLD_EDITOR is empty")
	}
	args := make([]string, len(parts))
	for i, p := range parts {
		p = strings.ReplaceAll(p, "{file}", file)
		p = strings.ReplaceAll(p, "{line}", line)
		args[i] = p
	}
	// Build argv directly (no shell) so paths with spaces stay one argument.
	return exec.Command(args[0], args[1:]...).Start()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"target":   s.target,
		"diff":     s.differ != nil,
		"platform": model.HasPlatformView(s.engine),
		// A workspace unlocks the level above the service view.
		"workspace": model.HasWorkspace(s.engine),
	})
}

// GET /api/files — the indexed source files, for the file tree.
func (s *Server) handleFiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"files": s.engine.Files()})
}

// GET /api/symbol?name=<qualified-or-bare-name>
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: name")
		return
	}
	id, err := s.engine.LookupSymbol(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	frame, err := s.engine.Frame(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeFrame(w, frame)
}

// GET /api/body?targetId=<id>  OR  /api/body?callId=<id>[&choice=<int>]
//
// `choice` selects which candidate to expand for an interface call;
// it's ignored for direct calls. Defaults to 0 (the first candidate).
func (s *Server) handleBody(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	targetID := q.Get("targetId")
	callID := q.Get("callId")
	switch {
	case targetID != "" && callID != "":
		writeError(w, http.StatusBadRequest, "specify exactly one of targetId or callId")
	case targetID != "":
		frame, err := s.engine.Frame(model.TargetID(targetID))
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.writeFrame(w, frame)
	case callID != "":
		choice := 0
		if v := q.Get("choice"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				choice = n
			}
		}
		frame, err := s.engine.FrameForCall(model.CallID(callID), choice)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.writeFrame(w, frame)
	default:
		writeError(w, http.StatusBadRequest, "missing query param: targetId or callId")
	}
}

// GET /api/search?q=<substr>&limit=<int>
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": s.engine.Search(q, limit),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
