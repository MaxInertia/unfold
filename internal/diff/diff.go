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
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

// lineDiffCap bounds the O(n*m) LCS: above it we still report "modified" but
// skip per-line detail. Real function bodies are far smaller.
const lineDiffCap = 3000

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
	f.Diff = &model.FrameDiff{
		Status:     "modified",
		AddedLines: addedLines(baseFrame.Source, f.Source),
	}
}

// addedLines returns the 0-based indices of lines in head that are not part of
// the longest common subsequence of (base, head) lines — i.e. the new/changed
// lines a reviewer should focus on. Standard LCS backtrack.
func addedLines(base, head string) []int {
	bl := strings.Split(base, "\n")
	hl := strings.Split(head, "\n")
	n, m := len(bl), len(hl)
	if n*m > lineDiffCap || m == 0 {
		return nil
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

	var added []int
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case bl[i] == hl[j]:
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++ // base-only line (a removal); not shown in head
		default:
			added = append(added, j) // head-only line (added/changed)
			j++
		}
	}
	for ; j < m; j++ {
		added = append(added, j)
	}
	return added
}
