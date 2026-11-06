package diff

import (
	"reflect"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

func TestLineDiffRemoved(t *testing.T) {
	cases := []struct {
		name        string
		base, head  string
		wantAdded   []int
		wantRemoved []int
	}{
		{"identical", "a\nb", "a\nb", nil, nil},
		{"removal only", "a\nb\nc", "a\nc", nil, []int{1}},
		{"change line", "a\nb\nc", "a\nB\nc", []int{1}, []int{1}},
		{"trailing removal", "a\nb\nc", "a", nil, []int{1, 2}},
		{"replace all", "x\ny", "a\nb", []int{0, 1}, []int{0, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			added, removed := lineDiff(tc.base, tc.head)
			if !sameInts(added, tc.wantAdded) || !sameInts(removed, tc.wantRemoved) {
				t.Errorf("lineDiff = (%v, %v), want (%v, %v)", added, removed, tc.wantAdded, tc.wantRemoved)
			}
		})
	}
}

func sameInts(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// listingEngine is a stubEngine that can also enumerate its functions, which
// is what Changed needs of the head (and uses on the base to count deletions).
type listingEngine struct {
	stubEngine
	order []model.TargetID
}

func (l listingEngine) Functions() []model.SearchResult {
	out := make([]model.SearchResult, 0, len(l.order))
	for n, id := range l.order {
		out = append(out, model.SearchResult{TargetID: id, Label: string(id), File: "a.go", Line: n + 1})
	}
	return out
}

func TestChanged(t *testing.T) {
	base := listingEngine{
		stubEngine: stubEngine{frames: map[model.TargetID]*model.Frame{
			"pkg.Same":     {ID: "pkg.Same", Source: "func Same() {\n\treturn\n}"},
			"pkg.Modified": {ID: "pkg.Modified", Source: "func Modified() {\n\told()\n\tkept()\n}"},
			"pkg.Gone":     {ID: "pkg.Gone", Source: "func Gone() {}"},
		}},
		order: []model.TargetID{"pkg.Same", "pkg.Modified", "pkg.Gone"},
	}
	head := listingEngine{
		stubEngine: stubEngine{frames: map[model.TargetID]*model.Frame{
			"pkg.Same":     {ID: "pkg.Same", Source: "func Same() {\n\treturn\n}"},
			"pkg.Modified": {ID: "pkg.Modified", Source: "func Modified() {\n\tnew()\n\tkept()\n\tmore()\n}"},
			"pkg.New":      {ID: "pkg.New", Source: "func New() {\n}"},
		}},
		order: []model.TargetID{"pkg.Same", "pkg.Modified", "pkg.New"},
	}

	changes, deleted, err := New(base).Changed(head)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (pkg.Gone)", deleted)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want Modified then New", changes)
	}
	m := changes[0]
	if m.ID != "pkg.Modified" || m.Status != "modified" || m.Added != 2 || m.Removed != 1 || !reflect.DeepEqual(m.AddedLines, []int{1, 3}) {
		t.Errorf("modified: got %+v", m)
	}
	n := changes[1]
	if n.ID != "pkg.New" || n.Status != "added" || n.Added != 2 || n.Removed != 0 {
		t.Errorf("added: got %+v", n)
	}

	t.Run("head that cannot list", func(t *testing.T) {
		if _, _, err := New(base).Changed(head.stubEngine); err != ErrNoFunctionList {
			t.Errorf("err = %v, want ErrNoFunctionList", err)
		}
	})
}
