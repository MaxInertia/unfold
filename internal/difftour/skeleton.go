package difftour

import (
	"fmt"
	"strings"

	"github.com/MaxInertia/unfold/internal/tours"
)

// Skeleton renders a Reach as the outline of a tour an author will finish:
// the stops in execution order with their expansions, and in place of each
// description a cue placeholder on every changed line range, so the prose
// gets written around the lines that changed rather than discovering them.
// Title and description are left for the author too — the generated tour
// states facts; a story says why.
//
// Cues are relative to the stop's first line ("[[line:+3-5]]"), the form
// that survives the file moving. A function new on the branch has no changed
// range to cue — all of it is — so its description is simply empty.
func Skeleton(r *Reach, base string) tours.Tour {
	t := tours.Tour{
		Name:  "diff-story",
		Stops: make([]tours.Stop, 0, len(r.Functions)),
	}
	for _, f := range r.Functions {
		t.Stops = append(t.Stops, tours.Stop{
			Symbol:      f.Symbol,
			Expand:      f.Expand,
			Description: cuePlaceholders(f.AddedLines),
		})
	}
	return t
}

// cuePlaceholders turns 0-based changed line indices into "[[line:+a]]" and
// "[[line:+a-b]]" cues, one per run of consecutive lines, space-separated
// with a trailing space so prose can follow directly.
func cuePlaceholders(lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	var cues []string
	start, prev := lines[0], lines[0]
	flush := func() {
		if start == prev {
			cues = append(cues, fmt.Sprintf("[[line:+%d]]", start))
		} else {
			cues = append(cues, fmt.Sprintf("[[line:+%d-%d]]", start, prev))
		}
	}
	for _, l := range lines[1:] {
		if l == prev+1 {
			prev = l
			continue
		}
		flush()
		start, prev = l, l
	}
	flush()
	return strings.Join(cues, " ") + " "
}
