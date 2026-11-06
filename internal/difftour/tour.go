package difftour

import (
	"fmt"
	"strings"

	"github.com/MaxInertia/unfold/internal/tours"
)

// Tour renders a Reach as a tour: one stop per changed function in execution
// order, each pre-opened on the changed functions it leads to, described
// from the facts the index knows. It is generated, not written: the name
// can't be a file (":" and "/" are not allowed in one), so it never shadows
// a tour someone wrote.
//
// base is the ref the branch was compared with and commit the merge-base it
// resolved to; deleted is how many base functions the branch removed, which
// have nothing to visit and so are only counted.
func Tour(r *Reach, base, commit string, deleted int) tours.Tour {
	t := tours.Tour{
		Name:      "diff:" + base,
		Title:     "Changes vs " + base,
		Generated: true,
		Stops:     make([]tours.Stop, 0, len(r.Functions)),
	}
	var d []string
	n := len(r.Functions)
	d = append(d, fmt.Sprintf("The %s this branch changes vs %s%s, in execution order from the entrypoints that reach %s.",
		plural(n, "function"), base, shortCommit(commit), them(n)))
	switch {
	case !r.EntrypointsKnown:
		d = append(d, "This engine can't name where execution starts, so the order is source order.")
	case r.Unreached > 0 && r.Unreached == n:
		d = append(d, "None is reached from a recognized entrypoint.")
	case r.Unreached > 0:
		d = append(d, fmt.Sprintf("The last %s reached from a recognized entrypoint.", isAre(r.Unreached)))
	}
	if deleted > 0 {
		d = append(d, fmt.Sprintf("%s deleted (nothing to visit).", plural(deleted, "function")))
	}
	d = append(d, "Generated from the index; not a file in .unfold/tours/.")
	t.Description = strings.Join(d, " ")

	for _, f := range r.Functions {
		t.Stops = append(t.Stops, tours.Stop{
			Symbol:      f.Symbol,
			Expand:      f.Expand,
			Description: describe(f, r),
		})
	}
	return t
}

// describe writes a stop's description from what is known: the kind and size
// of the change, how execution gets here, and what changed code opens below.
func describe(f Function, r *Reach) string {
	var parts []string
	switch {
	case f.Status == "added":
		parts = append(parts, fmt.Sprintf("New on this branch, %d lines.", f.Added))
	case f.Added == 0 && f.Removed == 0:
		// Too large for line detail (see diff.lineDiffCap): the body differs,
		// and that is all that is known.
		parts = append(parts, "Changed.")
	default:
		parts = append(parts, fmt.Sprintf("Changed: +%d −%d lines.", f.Added, f.Removed))
	}

	switch {
	case len(f.Routes) == 0 && !r.EntrypointsKnown:
		// Nothing to say about reach that would be a claim.
	case len(f.Routes) == 0:
		parts = append(parts, "Not reached from any recognized entrypoint.")
	default:
		near := f.Routes[0]
		if len(near.Path) == 1 {
			parts = append(parts, fmt.Sprintf("An entrypoint itself (%s).", entryLabel(near)))
		} else {
			hops := make([]string, 0, len(near.Path)-2)
			for _, h := range near.Path[1 : len(near.Path)-1] {
				hops = append(hops, "[["+r.Names[h]+"]]")
			}
			via := ""
			if len(hops) > 0 {
				via = " via " + strings.Join(hops, " → ")
			}
			parts = append(parts, fmt.Sprintf("Reached from entrypoint [[%s]] (%s)%s.", r.Names[near.ID], entryLabel(near), via))
		}
		if others := len(f.Routes) - 1; others > 0 {
			names := make([]string, 0, 3)
			for _, rt := range f.Routes[1:] {
				if len(names) == 3 {
					names = append(names, "…")
					break
				}
				names = append(names, "[["+r.Names[rt.ID]+"]]")
			}
			parts = append(parts, fmt.Sprintf("Also from %s: %s.", plural(others, "other entrypoint"), strings.Join(names, ", ")))
		}
	}

	if len(f.Expand) > 0 {
		// Name the first few; an entrypoint can lead to dozens, and the
		// expansion itself shows them all.
		const shown = 5
		leaves := make([]string, 0, shown+1)
		for _, e := range f.Expand {
			if len(leaves) == shown {
				leaves = append(leaves, fmt.Sprintf("and %d more", len(f.Expand)-shown))
				break
			}
			hops := strings.Split(e, " > ")
			leaves = append(leaves, "[["+hops[len(hops)-1]+"]]")
		}
		parts = append(parts, fmt.Sprintf("Opened below: the changed %s it leads to, %s.", plural(len(f.Expand), "function"), strings.Join(leaves, ", ")))
	}
	return strings.Join(parts, " ")
}

// entryLabel names an entrypoint's kind in a parenthetical: "main",
// "http.route /api/health", "init".
func entryLabel(rt Route) string {
	if rt.Key != "" && rt.Kind != "init" {
		return rt.Kind + " " + rt.Key
	}
	return rt.Kind
}

func shortCommit(c string) string {
	if len(c) > 12 {
		c = c[:12]
	}
	if c == "" {
		return ""
	}
	return " (merge-base " + c + ")"
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func isAre(n int) string {
	if n == 1 {
		return "1 is not"
	}
	return fmt.Sprintf("%d are not", n)
}
