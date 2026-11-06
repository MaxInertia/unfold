package server

import (
	"net/http"
	"strings"

	"github.com/MaxInertia/unfold/internal/traces"
)

// SetTraces enables the execution-trace API backed by the given store.
func (s *Server) SetTraces(t *traces.Store) { s.traces = t }

// /api/traces — list the recorded runs in <project>/.unfold/traces/ without
// their events, and the directory `unfold trace` writes into. Read-only:
// traces are produced by the CLI, which instruments and runs a test, and
// that is not something a browser should be able to ask a server to do.
func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		writeError(w, http.StatusNotImplemented, "traces are not enabled")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	list := s.traces.List()
	if list == nil {
		list = []traces.Summary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": list, "dir": s.traces.Dir()})
}

// /api/traces/<name> — one trace with all its events.
func (s *Server) handleTrace(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		writeError(w, http.StatusNotImplemented, "traces are not enabled")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/traces/")
	if name == "" {
		s.handleTraces(w, r)
		return
	}
	t, err := s.traces.Get(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
