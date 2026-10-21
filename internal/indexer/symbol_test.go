package indexer

import "testing"

// The forms a reader — or a tour written by a model — spells a symbol in,
// against the full names the index keys on.
func TestMatchesSymbol(t *testing.T) {
	const fn = "github.com/x/internal/tours.NewStore"
	const method = "(*github.com/x/internal/tours.Store).Save"
	const valueMethod = "(github.com/x/internal/tours.Stop).Where"

	yes := map[string][]string{
		fn:          {"NewStore", "tours.NewStore", "internal/tours.NewStore", fn},
		method:      {"Save", "Store.Save", "tours.Store.Save", "(*Store).Save", "tours.(*Store).Save", "(*tours.Store).Save", "internal/tours.Store.Save", method},
		valueMethod: {"Where", "Stop.Where", "(Stop).Where", "tours.Stop.Where"},
	}
	no := map[string][]string{
		fn:     {"Store", "tours", "ewStore", "notes.NewStore", "", "NewStore.x"},
		method: {"AutoSave", "Stop.Save", "notes.Store.Save", "tours.Store", "(*Store)"},
	}
	for full, queries := range yes {
		for _, q := range queries {
			if !matchesSymbol(full, q) {
				t.Errorf("matchesSymbol(%q, %q) = false, want true", full, q)
			}
		}
	}
	for full, queries := range no {
		for _, q := range queries {
			if matchesSymbol(full, q) {
				t.Errorf("matchesSymbol(%q, %q) = true, want false", full, q)
			}
		}
	}
}
