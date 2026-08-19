package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Set is a merged rule set plus everything that went wrong assembling it.
//
// Problems are carried rather than returned as a fatal error: a bad rule in a
// shared org file must not stop someone reading code, and a silently dropped
// rule is exactly the failure this package exists to avoid. They surface in
// the UI instead.
type Set struct {
	Rules []Rule
	// Serves is what the files declare their services serve, in file order.
	// Unlike rules these are not merged by id: two files declaring the same
	// service's surface are two statements about it, and the union is what
	// both of them meant.
	Serves   []Serve
	Problems []string
	// Sources records which file each rule id came from, so "why is this edge
	// here" is answerable down to the file someone else committed.
	Sources map[string]string
}

// isRepoLocal reports whether a rules file is a repository's own, which is the
// only file whose unnamed statements have a subject.
func isRepoLocal(path string) bool {
	return path != "" && path == RepoPath(filepath.Dir(filepath.Dir(path)))
}

// ServesFor returns the entries that apply to one service, matched by any of
// the names it goes by — its directory and whatever its manifest calls it.
//
// A repo-local file's unnamed entries are about the repository they are in,
// which is how they were validated, so they apply wherever that file was read.
func (s Set) ServesFor(dir string, names ...string) []Serve {
	want := map[string]bool{}
	for _, n := range names {
		if n != "" {
			want[n] = true
		}
	}
	local := RepoPath(dir)
	var out []Serve
	for _, sv := range s.Serves {
		if sv.Service == "" {
			if local != "" && sv.From == local {
				out = append(out, sv)
			}
			continue
		}
		if want[sv.Service] {
			out = append(out, sv)
		}
	}
	return out
}

// Disabled reports the ids switched off, including built-ins.
func (s Set) Disabled() map[string]bool {
	out := map[string]bool{}
	for _, r := range s.Rules {
		if !r.On() {
			out[r.ID] = true
		}
	}
	return out
}

// Load reads rule files in precedence order — later files win — and merges
// them by id.
//
// The order is org, then repo, then personal: a shared file establishes what a
// library looks like, a repo overrides it for local reality, and a personal
// file is the last word for the one machine. Merging by id is what makes
// "switch off a built-in here" work: the entry doesn't have to restate the
// rule, only name it.
func Load(paths ...string) Set {
	set := Set{Sources: map[string]string{}}
	byID := map[string]int{}

	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			// A missing file at any tier is normal — most projects will have
			// none of them — so only a real read failure is worth reporting.
			if !errors.Is(err, os.ErrNotExist) {
				set.Problems = append(set.Problems, fmt.Sprintf("%s: %v", path, err))
			}
			continue
		}
		f, problems, err := Parse(data)
		if err != nil {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		for _, p := range problems {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: %v", path, p))
		}
		for _, r := range f.Rules {
			if at, ok := byID[r.ID]; ok {
				set.Rules[at] = merge(set.Rules[at], r)
			} else {
				byID[r.ID] = len(set.Rules)
				set.Rules = append(set.Rules, r)
			}
			set.Sources[r.ID] = path
		}
		for _, sv := range f.Serves {
			// A shared file's entry has to name the service it is about: with
			// no name it would mean "this file's repository", and a file
			// applied to every repository would make every one of them serve
			// it. Repo-local files are the last path in the list, which is
			// where an unnamed entry is answerable.
			if sv.Service == "" && !isRepoLocal(path) {
				set.Problems = append(set.Problems, fmt.Sprintf(
					"%s: a serves entry in a shared file must name its service, "+
						"or every repository would be said to serve it", path))
				continue
			}
			sv.From = path
			set.Serves = append(set.Serves, sv)
		}
	}

	for _, err := range Validate(set.Rules) {
		set.Problems = append(set.Problems, err.Error())
	}
	return set
}

// merge lets a later tier change part of a rule without restating it — most
// often just `{"id": "...", "enabled": false}`.
func merge(base, over Rule) Rule {
	out := base
	if over.Enabled != nil {
		out.Enabled = over.Enabled
	}
	if over.Match != nil {
		out.Match = over.Match
	}
	if over.Emit != nil {
		out.Emit = over.Emit
	}
	if over.Comment != "" {
		out.Comment = over.Comment
	}
	return out
}

// RepoPath is the per-repo rules file, beside the other .unfold state.
func RepoPath(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ".unfold", "recognizers.json")
}

// UserPath is the personal rules file. Empty when there's no home directory to
// put one in.
func UserPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "unfold", "recognizers.json")
}
