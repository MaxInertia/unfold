// Package gitbase resolves a diff base and materializes it as a throwaway git
// worktree so the indexer can load the base revision the same way it loads the
// working tree.
package gitbase

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// MergeBase returns the merge-base commit of HEAD and ref within repoDir — i.e.
// the point the current branch diverged from ref. Diffing against this (rather
// than ref's tip) yields the "what this branch changes" view a reviewer wants.
func MergeBase(repoDir, ref string) (string, error) {
	out, err := run(repoDir, "merge-base", "HEAD", ref)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "", fmt.Errorf("no merge-base between HEAD and %q", ref)
	}
	return commit, nil
}

// AddWorktree checks commit out into a fresh temp directory via `git worktree
// add --detach`. The returned cleanup removes the worktree (call on shutdown).
func AddWorktree(repoDir, commit string) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "unfold-base-*")
	if err != nil {
		return "", nil, err
	}
	if _, err := run(repoDir, "worktree", "add", "--detach", tmp, commit); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	cleanup = func() {
		// `worktree remove` unregisters it; RemoveAll is belt-and-suspenders in
		// case the dir lingers (e.g. remove refused on a dirty tree).
		_, _ = run(repoDir, "worktree", "remove", "--force", tmp)
		_ = os.RemoveAll(tmp)
	}
	return tmp, cleanup, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
