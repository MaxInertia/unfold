package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

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
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/body", s.handleBody)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.Handle("/", http.FileServer(http.FS(s.static)))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "target": s.target})
}

func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w) }
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request)   { writeNotImplemented(w) }
func (s *Server) handleBody(w http.ResponseWriter, r *http.Request)   { writeNotImplemented(w) }
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeNotImplemented(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "not implemented"})
}
