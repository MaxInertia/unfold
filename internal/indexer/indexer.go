// Package indexer loads Go packages and builds the call-site and
// implementer indexes that power unfold's interactive code expansion.
//
// Phase 1 stub — Load is a no-op.
package indexer

// Indexer holds the loaded packages and indexes for a Go module.
type Indexer struct{}

// New returns a fresh indexer with no packages loaded.
func New() *Indexer { return &Indexer{} }

// Load loads the Go packages matched by the given pattern (e.g. "./...")
// and builds the call-site and implementer indexes.
//
// TODO(phase 1): implement.
func (i *Indexer) Load(pattern string) error {
	_ = pattern
	return nil
}
