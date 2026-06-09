package diff

import (
	"reflect"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

func TestAddedLines(t *testing.T) {
	cases := []struct {
		name string
		base string
		head string
		want []int
	}{
		{"identical", "a\nb\nc", "a\nb\nc", nil},
		{"append", "a\nb", "a\nb\nc", []int{2}},
		{"insert middle", "a\nc", "a\nb\nc", []int{1}},
		{"change line", "a\nb\nc", "a\nB\nc", []int{1}},
		{"prepend", "b\nc", "a\nb\nc", []int{0}},
		{"all new", "x\ny", "a\nb", []int{0, 1}},
		{"removal only", "a\nb\nc", "a\nc", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := addedLines(tc.base, tc.head)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("addedLines = %v, want %v", got, tc.want)
			}
		})
	}
}

// stubEngine implements just enough of model.Engine to drive Annotate.
type stubEngine struct {
	frames map[model.TargetID]*model.Frame
}

func (s stubEngine) Frame(id model.TargetID) (*model.Frame, error) {
	if f, ok := s.frames[id]; ok {
		return f, nil
	}
	return nil, errNotFound
}
func (s stubEngine) LookupSymbol(string) (model.TargetID, error)           { return "", errNotFound }
func (s stubEngine) FrameForCall(model.CallID, int) (*model.Frame, error)  { return nil, errNotFound }
func (s stubEngine) Search(string, int) []model.SearchResult               { return nil }
func (s stubEngine) Files() []string                                       { return nil }
func (s stubEngine) TypeInfo(model.TargetID, int) (*model.TypeInfo, error) { return nil, nil }

type sentinel string

func (e sentinel) Error() string { return string(e) }

const errNotFound = sentinel("not found")

func TestAnnotate(t *testing.T) {
	base := stubEngine{frames: map[model.TargetID]*model.Frame{
		"pkg.Same":     {ID: "pkg.Same", Source: "func Same() {\n\treturn\n}"},
		"pkg.Modified": {ID: "pkg.Modified", Source: "func Modified() {\n\told()\n}"},
	}}
	d := New(base)

	t.Run("unchanged", func(t *testing.T) {
		f := &model.Frame{ID: "pkg.Same", Source: "func Same() {\n\treturn\n}"}
		d.Annotate(f)
		if f.Diff == nil || f.Diff.Status != "unchanged" {
			t.Fatalf("got %+v, want unchanged", f.Diff)
		}
	})
	t.Run("modified", func(t *testing.T) {
		f := &model.Frame{ID: "pkg.Modified", Source: "func Modified() {\n\tnew()\n}"}
		d.Annotate(f)
		if f.Diff == nil || f.Diff.Status != "modified" || !reflect.DeepEqual(f.Diff.AddedLines, []int{1}) {
			t.Fatalf("got %+v, want modified line 1", f.Diff)
		}
	})
	t.Run("added", func(t *testing.T) {
		f := &model.Frame{ID: "pkg.New", Source: "func New() {}"}
		d.Annotate(f)
		if f.Diff == nil || f.Diff.Status != "added" {
			t.Fatalf("got %+v, want added", f.Diff)
		}
	})
	t.Run("file frame skipped", func(t *testing.T) {
		f := &model.Frame{ID: "file:/x/y.go", Source: "package y"}
		d.Annotate(f)
		if f.Diff != nil {
			t.Fatalf("file frame should not be annotated, got %+v", f.Diff)
		}
	})
}
