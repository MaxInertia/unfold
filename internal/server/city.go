package server

import (
	"errors"
	"net/http"
	"sync"

	"github.com/MaxInertia/unfold/internal/model"
)

// cityCache holds the last /api/city answer per index build. The engine's
// own view is memoized and is the same pointer for as long as the index it
// came from is current, so that pointer is the cache key: a reload yields a
// new view, and the diff annotation — the expensive half, one frame read per
// function in head and base — is redone exactly then.
type cityCache struct {
	mu   sync.Mutex
	repo string
	base *model.CityView // the engine's view the entry was derived from
	view *model.CityView
}

// GET /api/city[?repo=<alias>] — every package of one repository with its
// functions: name, file, line count, fan-in, fan-out, and in diff mode
// whether the function changed. The frontend lays it out; see web/src/city.ts
// for the rule and the stability it guarantees.
func (s *Server) handleCity(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	var (
		view *model.CityView
		err  error
	)
	if repo != "" {
		co, ok := s.engine.(model.CityOfEngine)
		if !ok {
			writeError(w, http.StatusNotImplemented, model.ErrNoWorkspace.Error())
			return
		}
		view, err = co.CityOf(repo)
	} else {
		ce, ok := s.engine.(model.CityEngine)
		if !ok {
			writeError(w, http.StatusNotImplemented, model.ErrNoCity.Error())
			return
		}
		view, err = ce.City()
	}
	if err != nil {
		switch {
		case errors.Is(err, model.ErrNoCity), errors.Is(err, model.ErrNoWorkspace):
			writeError(w, http.StatusNotImplemented, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, s.cityWithDiff(repo, view))
}

// cityWithDiff marks the changed functions when diff mode is on. Only the
// primary repo is compared: the base engine indexes the merge-base of the
// repo unfold was started in, and a workspace sibling's ids name functions
// it has never seen, which would make every one of them look new.
func (s *Server) cityWithDiff(repo string, view *model.CityView) *model.CityView {
	if s.differ == nil || repo != "" {
		return view
	}
	s.city.mu.Lock()
	defer s.city.mu.Unlock()
	if s.city.base == view && s.city.repo == repo {
		return s.city.view
	}
	out := &model.CityView{Module: view.Module, Root: view.Root, Packages: make([]model.CityPackage, len(view.Packages))}
	for pi, p := range view.Packages {
		funcs := make([]model.CityFunc, len(p.Funcs))
		for fi, f := range p.Funcs {
			if frame, err := s.engine.Frame(f.ID); err == nil {
				s.differ.Annotate(frame)
				f.Changed = frame.Diff != nil && frame.Diff.Status != "unchanged"
			}
			funcs[fi] = f
		}
		out.Packages[pi] = model.CityPackage{Path: p.Path, Name: p.Name, Funcs: funcs}
	}
	s.city.repo, s.city.base, s.city.view = repo, view, out
	return out
}
