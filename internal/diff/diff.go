// Package diff annotates frames with how they differ from a base revision.
//
// It holds a second "base engine" (the project indexed at the diff base — see
// the --diff-base flag) and, for a given head frame, looks up the same function
// in the base by target id. Go target ids are package-qualified names
// (obj.FullName()), so the same function has the same id in both engines even
// though they're rooted at different directories — which is what makes
// cross-revision matching work without positional fragility. (The TS engine's
// ids are positional, so diff is Go-only until name-based identity lands.)
package diff

import (
	"errors"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

// lineDiffCap bounds the O(n*m) LCS table (in cells): above it we still report
// "modified" but skip per-line detail. A million cells is two 1000-line
// bodies — 8 MB, transient — which is well past any function a reader wants
// line detail for, while the old cap of 3000 cut out at about 55 lines a side
// and left every ordinary handler reporting no counts at all.
const lineDiffCap = 1_000_000

// Differ attaches model.FrameDiff to frames by comparing against a base engine.
type Differ struct {
	base model.Engine
}

// New returns a Differ backed by base (the project indexed at the diff base).
func New(base model.Engine) *Differ {
	return &Differ{base: base}
}

// Annotate sets f.Diff describing how f differs from the same function in the
// base. File frames (whole-file views) are left unannotated — their ids are
// path-based and don't match across the two engine roots.
func (d *Differ) Annotate(f *model.Frame) {
	if f == nil || d == nil || d.base == nil {
		return
	}
	if strings.HasPrefix(string(f.ID), "file:") {
		return
	}
	baseFrame, err := d.base.Frame(f.ID)
	if err != nil || baseFrame == nil {
		// No matching function in the base → this function is new on the branch.
		f.Diff = &model.FrameDiff{Status: "added"}
		return
	}
	if baseFrame.Source == f.Source {
		f.Diff = &model.FrameDiff{Status: "unchanged"}
		return
	}
	added, _ := lineDiff(baseFrame.Source, f.Source)
	f.Diff = &model.FrameDiff{
		Status:     "modified",
		AddedLines: added,
	}
}

// Change is one function the branch touched, with the counts a reviewer
// skims first. AddedLines is the same 0-based detail Annotate attaches, kept
// here so a consumer that lists changes doesn't have to open each frame again
// to learn where in it the change sits.
type Change struct {
	ID    model.TargetID `json:"id"`
	Title string         `json:"title"`
	File  string         `json:"file"`
	Line  int            `json:"line"`
	// Status is "added" (not in the base) or "modified".
	Status     string `json:"status"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	AddedLines []int  `json:"addedLines,omitempty"`
}

// ErrNoFunctionList is returned by Changed when the head engine can't
// enumerate its functions, which is what listing the changes needs.
var ErrNoFunctionList = errors.New("the engine can't list its functions, so the changed set is unknown")

// Changed lists every function of head that differs from the base, in head's
// source order, plus how many functions the base had that head no longer
// does. A deleted function has nothing to open on this branch, so it is
// counted rather than listed.
//
// Frame identity is by target id, as in Annotate: the two engines are rooted
// at different directories, so this is a name match, not a positional one.
func (d *Differ) Changed(head model.Engine) (changes []Change, deleted int, err error) {
	lister, ok := head.(model.FunctionLister)
	if !ok || d == nil || d.base == nil {
		return nil, 0, ErrNoFunctionList
	}
	seen := map[model.TargetID]bool{}
	for _, fn := range lister.Functions() {
		seen[fn.TargetID] = true
		hf, err := head.Frame(fn.TargetID)
		if err != nil || hf == nil {
			continue
		}
		c := Change{ID: fn.TargetID, Title: fn.Label, File: fn.File, Line: fn.Line}
		bf, err := d.base.Frame(fn.TargetID)
		switch {
		case err != nil || bf == nil:
			c.Status = "added"
			c.Added = len(strings.Split(hf.Source, "\n"))
		case bf.Source == hf.Source:
			continue
		default:
			c.Status = "modified"
			added, removed := lineDiff(bf.Source, hf.Source)
			c.Added, c.Removed, c.AddedLines = len(added), len(removed), added
		}
		changes = append(changes, c)
	}
	if bl, ok := d.base.(model.FunctionLister); ok {
		for _, fn := range bl.Functions() {
			if !seen[fn.TargetID] {
				deleted++
			}
		}
	}
	return changes, deleted, nil
}

// lineDiff returns the 0-based indices of lines in head that are not part of
// the longest common subsequence of (base, head) lines — the new/changed lines
// a reviewer should focus on — and the base lines that likewise aren't, i.e.
// what was removed. Standard LCS backtrack.
func lineDiff(base, head string) (added, removed []int) {
	bl := strings.Split(base, "\n")
	hl := strings.Split(head, "\n")
	n, m := len(bl), len(hl)
	if n*m > lineDiffCap || m == 0 {
		return nil, nil
	}

	// dp[i][j] = length of LCS of bl[i:] and hl[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bl[i] == hl[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case bl[i] == hl[j]:
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			removed = append(removed, i) // base-only line; not shown in head
			i++
		default:
			added = append(added, j) // head-only line (added/changed)
			j++
		}
	}
	for ; i < n; i++ {
		removed = append(removed, i)
	}
	for ; j < m; j++ {
		added = append(added, j)
	}
	return added, removed
}

// addedLines is lineDiff's head side, kept for the tests that pin the
// highlight behaviour.
func addedLines(base, head string) []int {
	added, _ := lineDiff(base, head)
	return added
}
