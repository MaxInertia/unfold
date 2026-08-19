package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

// Diff mode's base is a throwaway worktree of this repository at the
// merge-base. Loaded the ordinary way with a workspace open, it built a second
// whole workspace out of the *head* checkouts, found the worktree in none of
// them, and silently made the alphabetically first repo the primary — so the
// revision being diffed against was never indexed, and every frame in the repo
// being read had no counterpart and came back "added".
func TestDiffBaseLoadsAloneInsideAWorkspace(t *testing.T) {
	ws, err := filepath.Abs("../workspace/testdata/ws")
	if err != nil {
		t.Fatal(err)
	}
	prev := Workspace
	t.Cleanup(func() { Workspace = prev })
	Workspace = ws

	baseDir := t.TempDir()
	write(t, filepath.Join(baseDir, "go.mod"), "module example.com/inbox\n\ngo 1.22\n")
	write(t, filepath.Join(baseDir, "main.go"), "package main\n\nfunc Handle() {}\n\nfunc main() { Handle() }\n")

	base, err := LoadSolo(LangGo, baseDir, "./...")
	if err != nil {
		t.Fatalf("LoadSolo: %v", err)
	}
	if r, ok := base.(interface{ Repos() []model.RepoInfo }); ok {
		t.Fatalf("the base must be one repository, got a workspace of %d", len(r.Repos()))
	}
	// And it is the worktree that got indexed, not a sibling.
	if _, err := base.Frame("example.com/inbox.Handle"); err != nil {
		t.Errorf("the base revision's own code should be in the base index: %v", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
