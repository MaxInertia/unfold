package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the package-level workspace settings at a temporary root and
// restores them afterwards; they're process-wide because they must survive the
// engine rebuilds watch mode performs, which makes them shared state here.
func isolate(t *testing.T, workspace string) {
	t.Helper()
	prevWorkspace, prevLinked := Workspace, LinkedRepos
	t.Cleanup(func() { Workspace, LinkedRepos = prevWorkspace, prevLinked })
	Workspace, LinkedRepos = workspace, nil
}

// goDirs returning an empty list means "stay a plain single-repo index", so a
// --workspace that discovers nothing must be an error instead. Swallowing it
// produced a session that looked ordinary and silently had no platform level,
// no zoom-out, and no explanation on the terminal.
func TestGoDirsReportsUndiscoverableWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "not-a-module"), 0o755); err != nil {
		t.Fatal(err)
	}
	isolate(t, root)

	dirs, err := goDirs(root)
	if err == nil {
		t.Fatalf("goDirs(%q) = %v, nil; want an error naming the empty workspace", root, dirs)
	}
	// The message has to carry the flag and the path: the whole failure is that
	// the user pointed at the wrong directory, and they can't see which one the
	// process resolved.
	if !strings.Contains(err.Error(), "--workspace") || !strings.Contains(err.Error(), root) {
		t.Errorf("error %q names neither the flag nor %s", err, root)
	}
}

// The single-repo path is the one goDirs signals with an empty list, and it
// must stay distinguishable from the failure above.
func TestGoDirsPlainRepoIsNotAnError(t *testing.T) {
	isolate(t, "")

	dirs, err := goDirs(t.TempDir())
	if err != nil {
		t.Fatalf("goDirs without a workspace: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("goDirs = %v; want none, the signal to stay single-repo", dirs)
	}
}

// A workspace whose modules are found still yields them, so the new error
// path can't be passing by refusing everything.
func TestGoDirsFindsModules(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"gateway", "orders"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mod := "module " + name + "\n\ngo 1.22\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	isolate(t, root)

	dirs, err := goDirs(root)
	if err != nil {
		t.Fatalf("goDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Errorf("goDirs = %v; want the two modules under %s", dirs, root)
	}
}
