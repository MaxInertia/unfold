package engine

import "github.com/MaxInertia/unfold/internal/model"

// City forwards the primary repository's map to the current engine when it
// can draw one. The wrapper always satisfies model.CityEngine (method sets
// are static), so an engine without the facility is reported at call time.
func (r *Reloadable) City() (*model.CityView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ce, ok := r.cur.(model.CityEngine)
	if !ok {
		return nil, model.ErrNoCity
	}
	return ce.City()
}

// CityOf forwards the map of a named workspace service. Without a workspace
// the only repo is the primary one, so an empty name still answers; a
// non-empty one has nothing to resolve against.
func (r *Reloadable) CityOf(repo string) (*model.CityView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if co, ok := r.cur.(model.CityOfEngine); ok {
		return co.CityOf(repo)
	}
	if repo != "" {
		return nil, model.ErrNoWorkspace
	}
	ce, ok := r.cur.(model.CityEngine)
	if !ok {
		return nil, model.ErrNoCity
	}
	return ce.City()
}
