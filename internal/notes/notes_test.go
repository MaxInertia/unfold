package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	saved, err := s.Upsert(Note{
		Anchor: Anchor{File: "/p/a.go", Kind: "after-line", StartLine: 3, EndLine: 3, Snippet: "x := 1"},
		Text:   "see [[RunGreeter]]",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.ID == "" || saved.CreatedAt == "" {
		t.Fatalf("missing id/createdAt: %+v", saved)
	}

	// Update keeps identity and CreatedAt.
	saved.Text = "edited"
	updated, err := s.Upsert(saved)
	if err != nil {
		t.Fatalf("Upsert(update): %v", err)
	}
	if updated.ID != saved.ID || updated.CreatedAt != saved.CreatedAt {
		t.Errorf("update changed identity: %+v vs %+v", updated, saved)
	}

	// A fresh store reads the persisted file.
	s2 := NewStore(dir)
	got := s2.List()
	if len(got) != 1 || got[0].Text != "edited" {
		t.Fatalf("reload: got %+v", got)
	}

	// The file lives under .unfold/ and is human-readable JSON.
	buf, err := os.ReadFile(filepath.Join(dir, ".unfold", "notes.json"))
	if err != nil {
		t.Fatalf("notes.json: %v", err)
	}
	if !strings.Contains(string(buf), "edited") {
		t.Errorf("persisted file missing note text: %s", buf)
	}

	if err := s2.Delete(got[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := len(NewStore(dir).List()); n != 0 {
		t.Errorf("after delete: %d notes", n)
	}
}

func TestValidate(t *testing.T) {
	s := NewStore(t.TempDir())
	bad := []Anchor{
		{Kind: "after-line", StartLine: 1, EndLine: 1},             // no file
		{File: "/p/a.go", Kind: "after-line"},                      // no lines
		{File: "/p/a.go", Kind: "range", StartLine: 5, EndLine: 2}, // inverted
		{File: "/p/a.go", Kind: "sideways"},                        // unknown kind
	}
	for _, a := range bad {
		if _, err := s.Upsert(Note{Anchor: a, Text: "x"}); err == nil {
			t.Errorf("anchor %+v should be rejected", a)
		}
	}
	if _, err := s.Upsert(Note{Anchor: Anchor{File: "/p/a.go", Kind: "file-end"}, Text: "x"}); err != nil {
		t.Errorf("file-end anchor rejected: %v", err)
	}
	if err := s.Delete("nope"); err == nil {
		t.Error("Delete(unknown) should error")
	}
	if _, err := s.Upsert(Note{ID: "nope", Anchor: Anchor{File: "/p/a.go", Kind: "file-start"}}); err == nil {
		t.Error("Upsert(unknown id) should error")
	}
}
