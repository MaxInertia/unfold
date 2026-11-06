package workspace

import (
	"fmt"

	"github.com/MaxInertia/unfold/internal/model"
)

var (
	_ model.CityEngine   = (*Workspace)(nil)
	_ model.CityOfEngine = (*Workspace)(nil)
)

// City is the primary repository's map.
func (w *Workspace) City() (*model.CityView, error) { return w.CityOf(w.primary) }

// CityOf draws one service of the workspace. Only indexed code has functions
// to show, so asking for a lazily opened repo indexes it first — the same
// cost as following a call into it, paid for the same reason.
//
// Ids are qualified the way every other answer about that repo is, so a
// click on a building in another service's city opens the frame the rest of
// the API knows by that name. The primary repo's ids stay bare, which is
// what keeps single-repo links valid in a workspace.
func (w *Workspace) CityOf(repo string) (*model.CityView, error) {
	if repo == "" {
		repo = w.primary
	}
	idx, _, err := w.engineFor(repo + Sep)
	if err != nil {
		return nil, fmt.Errorf("city of %s: %w", repo, err)
	}
	view, err := idx.City()
	if err != nil {
		return nil, err
	}
	if repo == w.primary {
		return view, nil
	}
	// The indexer's view is memoized and shared, so the qualified copy is a
	// copy rather than a prefix written into it.
	out := &model.CityView{Module: view.Module, Root: view.Root, Packages: make([]model.CityPackage, len(view.Packages))}
	for pi, p := range view.Packages {
		funcs := make([]model.CityFunc, len(p.Funcs))
		for fi, f := range p.Funcs {
			f.ID = model.TargetID(w.qualify(repo, string(f.ID)))
			funcs[fi] = f
		}
		out.Packages[pi] = model.CityPackage{Path: p.Path, Name: p.Name, Funcs: funcs}
	}
	return out, nil
}
