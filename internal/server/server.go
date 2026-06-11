package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/model"
)

//go:embed all:static/dist
var staticFS embed.FS

type Server struct {
	engine model.Engine
	static fs.FS
	target string
	differ *diff.Differ // nil = diff mode off

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
		"status": "ok",
		"target": s.target,
		"diff":   s.differ != nil,
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
