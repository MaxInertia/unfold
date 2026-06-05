package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

//go:embed all:static/dist
var staticFS embed.FS

type Server struct {
	engine model.Engine
	static fs.FS
	target string
}

// New builds a server backed by any indexing engine (Go or TypeScript).
func New(engine model.Engine) *Server {
	sub, err := fs.Sub(staticFS, "static/dist")
	if err != nil {
		panic(err)
	}
	return &Server{engine: engine, static: sub}
}

// SetTarget records the indexer pattern (e.g. "./...") for the /api/health response.
func (s *Server) SetTarget(target string) { s.target = target }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/symbol", s.handleSymbol)
	mux.HandleFunc("/api/body", s.handleBody)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/typeinfo", s.handleTypeInfo)
	mux.HandleFunc("/api/open", s.handleOpen)
	mux.Handle("/", http.FileServer(http.FS(s.static)))
	return mux
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

// GET /api/open?file=<abs-path>&line=<n> — opens the file in the configured
// editor. The command comes from $UNFOLD_EDITOR (a template with {file} and
// {line}); it defaults to VS Code's "code -g {file}:{line}".
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	file := q.Get("file")
	if file == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: file")
		return
	}
	if err := openInEditor(file, q.Get("line")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "target": s.target})
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
	writeJSON(w, http.StatusOK, frame)
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
		writeJSON(w, http.StatusOK, frame)
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
		writeJSON(w, http.StatusOK, frame)
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
