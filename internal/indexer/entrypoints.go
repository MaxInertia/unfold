package indexer

import (
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
)

// Entrypoints lists where execution starts in this project's own code: the
// handlers behind inbound bindings (routes, RPC implementations, subscribers),
// main, init functions, and package-level initializers.
//
// This is deliberately narrower than the seed set entrypointReachable walks
// from. That walk counts every function of a main package so that an RPC
// called only from a command is never missed — an over-approximation that is
// right when the question is "is this call ever made". Here the question is
// "which ways in lead to this function", and the answer a reader wants names
// main, not main's helpers: a helper is reached *through* main and shows up on
// the path, not as a way in of its own.
func (i *Indexer) Entrypoints() []model.Entrypoint {
	i.mu.RLock()
	defer i.mu.RUnlock()

	seen := map[TargetID]bool{}
	var out []model.Entrypoint
	add := func(id TargetID, kind, key string) {
		fi := i.funcs[id]
		if id == "" || seen[id] || fi == nil || !i.ownsCode(fi) {
			return
		}
		seen[id] = true
		pos := i.fset.Position(fi.node.Pos())
		out = append(out, model.Entrypoint{
			ID:    id,
			Title: fi.title,
			Kind:  kind,
			Key:   key,
			File:  pos.Filename,
			Line:  pos.Line,
		})
	}
	for _, b := range i.bindings {
		if b.Role != model.RoleInbound {
			continue
		}
		// An enumerated binding is served by one of its candidates and unfold
		// can't tell which, so each is a way in — the same stance the service
		// view takes when it marks reach.
		add(b.Target, b.Kind, b.Key)
		for _, c := range b.Candidates {
			add(c.TargetID, b.Kind, b.Key)
		}
	}
	for id, fi := range i.funcs {
		switch {
		case fi.pkg != nil && fi.pkg.Name == "main" && fi.name == "main" && !fi.initializer:
			add(id, "main", "")
		case fi.initializer:
			add(id, "init", fi.title)
		case fi.name == "init":
			add(id, "init", "")
		}
	}
	// Source order, so the list is stable across runs and reads like the
	// project does; bindings come first within a file only when they were
	// declared first.
	sort.Slice(out, func(a, b int) bool {
		if out[a].File != out[b].File {
			return out[a].File < out[b].File
		}
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// Functions lists every function and method of the project's own code that
// has a body to show, in source order. Label is the display title, since the
// callers of this want something to name a function by rather than the id
// Search echoes.
func (i *Indexer) Functions() []model.SearchResult {
	i.mu.RLock()
	defer i.mu.RUnlock()

	out := make([]model.SearchResult, 0, len(i.funcs))
	for id, fi := range i.funcs {
		if fi.body == nil || !i.ownsCode(fi) {
			continue
		}
		pos := i.fset.Position(fi.node.Pos())
		out = append(out, model.SearchResult{
			TargetID: id,
			Label:    fi.title,
			File:     pos.Filename,
			Line:     pos.Line,
		})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].File != out[b].File {
			return out[a].File < out[b].File
		}
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].TargetID < out[b].TargetID
	})
	return out
}
