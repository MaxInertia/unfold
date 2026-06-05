package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestReloadablePicksUpNewSymbol verifies that Reload rebuilds the index so a
// function added to the source after startup becomes resolvable — the core
// payoff of watch mode.
func TestReloadablePicksUpNewSymbol(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmp\n\ngo 1.21\n")
	src := filepath.Join(dir, "main.go")
	writeFile(t, src, "package main\n\nfunc Alpha() {}\n\nfunc main() { Alpha() }\n")

	r, err := NewReloadable(LangGo, dir, "./...")
	if err != nil {
		t.Fatalf("NewReloadable: %v", err)
	}
	defer r.Close()

	if _, err := r.LookupSymbol("Alpha"); err != nil {
		t.Fatalf("Alpha should resolve before reload: %v", err)
	}
	if _, err := r.LookupSymbol("Beta"); err == nil {
		t.Fatal("Beta should not resolve before it is added")
	}

	// Add Beta and reindex.
	writeFile(t, src, "package main\n\nfunc Alpha() {}\n\nfunc Beta() {}\n\nfunc main() { Alpha(); Beta() }\n")
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := r.LookupSymbol("Beta"); err != nil {
		t.Fatalf("Beta should resolve after reload: %v", err)
	}
}

// TestWatcherFiresOnSourceChange checks that the debounced watcher invokes its
// callback for a source-file edit and ignores non-source files.
func TestWatcherFiresOnSourceChange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	writeFile(t, src, "package a\n")

	fired := make(chan struct{}, 8)
	w, err := NewWatcher(dir, 30*time.Millisecond, func() { fired <- struct{}{} })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	writeFile(t, src, "package a\n\nfunc F() {}\n")
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not fire on a .go change")
	}

	// Drain any coalesced extras, then confirm a non-source file is ignored.
	drain(fired)
	writeFile(t, filepath.Join(dir, "notes.txt"), "scratch\n")
	select {
	case <-fired:
		t.Fatal("watcher fired on a non-source file")
	case <-time.After(400 * time.Millisecond):
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func drain(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
