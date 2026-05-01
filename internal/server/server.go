package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/MaxInertia/unfold/internal/indexer"
)

//go:embed all:static/dist
var staticFS embed.FS

type Server struct {
	idx    *indexer.Indexer
	static fs.FS
	target string
}

func New(idx *indexer.Indexer) *Server {
	sub, err := fs.Sub(staticFS, "static/dist")
	if err != nil {
		panic(err)
	}
	return &Server{idx: idx, static: sub}
}

// SetTarget records the indexer pattern (e.g. "./...") for the /api/health response.
func (s *Server) SetTarget(target string) { s.target = target }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/symbol", s.handleSymbol)
	mux.HandleFunc("/api/body", s.handleBody)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.Handle("/", http.FileServer(http.FS(s.static)))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "target": s.target})
}

// GET /api/symbol?name=<qualified-or-bare-name>
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing required query param: name")
		return
	}
	id, err := s.idx.LookupSymbol(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	frame, err := s.idx.Frame(id)
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
		frame, err := s.idx.Frame(indexer.TargetID(targetID))
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
		frame, err := s.idx.FrameForCall(indexer.CallID(callID), choice)
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
		"results": s.idx.Search(q, limit),
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
