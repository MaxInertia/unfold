package indexer

import (
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
)

// Indexer implements the optional city half of the engine.
var _ model.CityEngine = (*Indexer)(nil)

// City returns every main-module function with its measurements. Computed
// once per index: the index is immutable after Load, and a reload builds a
// fresh Indexer, so there is nothing that could make a memoized answer stale.
//
// Only functions with a body are buildings. A declared-but-bodiless function
// (an assembly stub, an external linkname) has no frame to open, and a
// package-level initializer is indexed for reachability rather than for
// reading — neither is something a reader would click on a map to visit.
func (i *Indexer) City() (*model.CityView, error) {
	i.cityMu.Lock()
	defer i.cityMu.Unlock()
	if i.city != nil {
		return i.city, nil
	}
	i.city = i.buildCity()
	return i.city, nil
}

func (i *Indexer) buildCity() *model.CityView {
	i.mu.RLock()
	defer i.mu.RUnlock()

	// inRepo is the set of functions that are buildings; callee counts are
	// restricted to it so a call into the stdlib doesn't count as fan-out to
	// somewhere on the map.
	inRepo := make(map[TargetID]*funcInfo, len(i.funcs))
	for id, fi := range i.funcs {
		if fi.body == nil || fi.initializer || fi.obj == nil {
			continue
		}
		if fi.pkg == nil || fi.pkg.Module == nil || !fi.pkg.Module.Main {
			continue
		}
		inRepo[id] = fi
	}

	byPkg := make(map[string]*model.CityPackage)
	for id, fi := range inRepo {
		start := i.fset.Position(fi.node.Pos())
		end := i.fset.Position(fi.node.End())

		callers := make(map[TargetID]struct{})
		for _, u := range i.usagesByTarget[id] {
			// A value reference — the function passed as a callback — is a
			// use, but not a caller: whoever eventually invokes it is.
			if u.kind == model.UsageRef {
				continue
			}
			if u.parent != id {
				callers[u.parent] = struct{}{}
			}
		}

		callees := make(map[TargetID]struct{})
		for _, c := range fi.calls {
			if c.target != "" && c.target != id {
				if _, ok := inRepo[c.target]; ok {
					callees[c.target] = struct{}{}
				}
			}
			for _, cand := range c.candidates {
				if cand.TargetID == id {
					continue
				}
				if _, ok := inRepo[cand.TargetID]; ok {
					callees[cand.TargetID] = struct{}{}
				}
			}
		}

		p := byPkg[fi.pkg.PkgPath]
		if p == nil {
			p = &model.CityPackage{Path: fi.pkg.PkgPath, Name: fi.pkg.Name}
			byPkg[fi.pkg.PkgPath] = p
		}
		p.Funcs = append(p.Funcs, model.CityFunc{
			ID:      id,
			Name:    fi.title,
			File:    start.Filename,
			Lines:   end.Line - start.Line + 1,
			Callers: len(callers),
			Callees: len(callees),
		})
	}

	view := &model.CityView{Module: i.modulePath, Root: i.rootDir, Packages: []model.CityPackage{}}
	for _, p := range byPkg {
		// Name, then id: a title like "String" repeats across receivers'
		// methods ("Kind.String", "Mode.String" differ, but bare functions in
		// different files can't share a name — methods can share a title
		// only through their receiver), and the id is unique, so the order
		// is total and never depends on map walk.
		sort.Slice(p.Funcs, func(a, b int) bool {
			if p.Funcs[a].Name != p.Funcs[b].Name {
				return p.Funcs[a].Name < p.Funcs[b].Name
			}
			return p.Funcs[a].ID < p.Funcs[b].ID
		})
		view.Packages = append(view.Packages, *p)
	}
	sort.Slice(view.Packages, func(a, b int) bool {
		return view.Packages[a].Path < view.Packages[b].Path
	})
	return view
}
