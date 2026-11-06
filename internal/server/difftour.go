package server

import (
	"net/http"

	"github.com/MaxInertia/unfold/internal/difftour"
	"github.com/MaxInertia/unfold/internal/tours"
)

// diffTourCache is one computed answer to "what did this branch change, and
// what reaches it", held until the index is rebuilt.
type diffTourCache struct {
	reach   *difftour.Reach
	deleted int
	tour    tours.Tour
}

// diffTourResponse is what /api/diff/tour returns: the ordered changes with
// their reach, and the same facts rendered as a tour.
type diffTourResponse struct {
	Base   string `json:"base"`
	Commit string `json:"commit,omitempty"`
	*difftour.Reach
	Deleted int        `json:"deleted"`
	Tour    tours.Tour `json:"tour"`
}

// GET /api/diff/tour — the functions this branch changes vs the diff base, in
// execution order from the entrypoints that reach them, and the generated
// "Changes vs <base>" tour built from them. Only meaningful in diff mode: with
// no base there is no "changed", so the answer is a 404 that says how to get
// one rather than an empty list that reads as "nothing changed".
func (s *Server) handleDiffTour(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if s.differ == nil {
		writeError(w, http.StatusNotFound, "diff mode is off: start unfold with --diff-base <ref> to compare this branch against one")
		return
	}
	c, err := s.diffTourCached()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diffTourResponse{
		Base:    s.diffBase,
		Commit:  s.diffCommit,
		Reach:   c.reach,
		Deleted: c.deleted,
		Tour:    c.tour,
	})
}

// diffTourCached computes the changed set and its reach once per index. The
// lock is held across the computation so two tabs opening at once don't both
// pay for it; it is a few hundred milliseconds on a mid-sized repo.
func (s *Server) diffTourCached() (*diffTourCache, error) {
	s.diffTourMu.Lock()
	defer s.diffTourMu.Unlock()
	if s.diffTour != nil {
		return s.diffTour, nil
	}
	changes, deleted, err := s.differ.Changed(s.engine)
	if err != nil {
		return nil, err
	}
	reach := difftour.Compute(s.engine, changes)
	s.diffTour = &diffTourCache{
		reach:   reach,
		deleted: deleted,
		tour:    difftour.Tour(reach, s.diffBase, s.diffCommit, deleted),
	}
	return s.diffTour, nil
}
