package indexer

import (
	"path/filepath"
	"testing"
)

// A function named as a value is a site wherever it is named. The first shape
// — a package-level function handed to a registrar — already worked. A method
// value reached through a field (`foo.Bar.HandleFooBar`) is the same act of
// naming a body, and is how a subscriber registers a handler that lives on a
// struct, so it has to be a site too.
func TestMethodValuesAreReferenceSites(t *testing.T) {
	dir, err := filepath.Abs("testdata/nestedrefs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id, err := idx.LookupSymbol("register")
	if err != nil {
		t.Fatalf("LookupSymbol(register): %v", err)
	}
	frame, err := idx.Frame(id)
	if err != nil {
		t.Fatalf("Frame(register): %v", err)
	}

	var refs []CallSite
	lines := map[CallID]string{}
	for _, c := range frame.Calls {
		if c.Kind != KindRef {
			continue
		}
		refs = append(refs, c)
		lines[c.ID] = frame.Source[c.SpanStart:c.SpanEnd]
	}
	// One per registration in the fixture: a package-level function, and a
	// method value reached through a field, an interface, an embedded type and
	// a pointer. Every one of them names a body a reader may want to open.
	if len(refs) != 5 {
		t.Errorf("got %d reference sites, want 5: %v", len(refs), lines)
	}
	// And each expands to the body it names.
	for _, c := range refs {
		span := lines[c.ID]
		got, err := idx.FrameForCall(c.ID, 0)
		if err != nil {
			t.Errorf("FrameForCall(%s at %s): %v", span, c.ID, err)
			continue
		}
		if got.Title != span && got.Title != "Inner."+span {
			t.Errorf("%s expands to %q", span, got.Title)
		}
	}
}
