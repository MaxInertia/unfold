// Package notes persists user notes anchored to source locations. Notes
// live in a JSON file under the project root (.unfold/notes.json) rather
// than browser storage: they're knowledge capture, so they should survive
// the browser, be readable as plain text, and be committable when a team
// wants to share them.
package notes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Anchor pins a note to a location in file space (not frame space): a note
// renders in every frame whose line range contains it — function frames
// and whole-file frames alike.
type Anchor struct {
	File string `json:"file"` // absolute path of the anchored file
	// Kind is "after-line", "range", "file-start", or "file-end".
	Kind string `json:"kind"`
	// 1-based file lines. after-line uses StartLine==EndLine; range spans
	// [StartLine, EndLine]. Unused for file-start/file-end.
	StartLine int `json:"startLine,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
	// Snippet is the text of the anchor's last line at save time, so the
	// frontend can flag a note as drifted after the file is edited.
	Snippet string `json:"snippet,omitempty"`
}

// Note is one user note. Text may contain [[SymbolName]] / [[file:path]]
// references, which the frontend resolves and renders like code.
type Note struct {
	ID        string `json:"id"`
	Anchor    Anchor `json:"anchor"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// Store loads and persists the project's notes file. All methods are safe
// for concurrent use.
type Store struct {
	mu    sync.Mutex
	path  string
	notes []Note
	// nextID de-dupes ids created within the same nanosecond tick.
	nextID int
}

// NewStore opens (or lazily creates) the notes file for the project rooted
// at dir. A missing or unreadable file starts empty; the file and its
// .unfold/ directory are created on first write.
func NewStore(dir string) *Store {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	s := &Store{path: filepath.Join(dir, ".unfold", "notes.json")}
	if buf, err := os.ReadFile(s.path); err == nil {
		var loaded []Note
		if json.Unmarshal(buf, &loaded) == nil {
			s.notes = loaded
		}
	}
	return s
}

// List returns a copy of all notes.
func (s *Store) List() []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Note, len(s.notes))
	copy(out, s.notes)
	return out
}

// Upsert saves a note. An empty ID creates a new note; a known ID replaces
// that note's anchor and text. Timestamps are stamped here.
func (s *Store) Upsert(n Note) (Note, error) {
	if err := validate(n.Anchor); err != nil {
		return Note{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	if n.ID == "" {
		s.nextID++
		n.ID = fmt.Sprintf("n%d-%d", time.Now().UnixNano(), s.nextID)
		n.CreatedAt = now
		n.UpdatedAt = now
		s.notes = append(s.notes, n)
		return n, s.persist()
	}
	for i := range s.notes {
		if s.notes[i].ID == n.ID {
			n.CreatedAt = s.notes[i].CreatedAt
			n.UpdatedAt = now
			s.notes[i] = n
			return n, s.persist()
		}
	}
	return Note{}, fmt.Errorf("unknown note id %q", n.ID)
}

// Delete removes a note by id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notes {
		if s.notes[i].ID == id {
			s.notes = append(s.notes[:i], s.notes[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("unknown note id %q", id)
}

func validate(a Anchor) error {
	if a.File == "" {
		return fmt.Errorf("note anchor needs a file")
	}
	switch a.Kind {
	case "after-line", "range":
		if a.StartLine < 1 || a.EndLine < a.StartLine {
			return fmt.Errorf("note anchor needs 1-based startLine <= endLine")
		}
	case "file-start", "file-end":
		// no lines
	default:
		return fmt.Errorf("unknown anchor kind %q", a.Kind)
	}
	return nil
}

// persist writes the notes file atomically (tmp + rename), pretty-printed
// so a committed notes.json diffs sanely. Caller holds s.mu.
func (s *Store) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s.notes, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
