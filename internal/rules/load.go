package rules

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// BuiltinSource is what Sources records for a rule that shipped with unfold
// rather than coming from a file: the panel shows it as built-in, and toggling
// it writes a settings-only entry exactly as it does for a Go built-in.
const BuiltinSource = "built-in"

//go:embed builtin_effects.json
var builtinEffectsJSON []byte

var (
	builtinEffectsOnce sync.Once
	builtinEffects     []Rule
)

// BuiltinEffects is the shipped side-effect vocabulary: what in the standard
// library and the common drivers counts as touching the database, the
// network, the file system, the environment, the clock or the process.
//
// Shipped as data rather than Go for a reason the Go built-ins can't claim: a
// user adding "our in-house cache client is a database" writes exactly this
// shape, and seeing the shipped ones is how they learn it.
func BuiltinEffects() []Rule {
	builtinEffectsOnce.Do(func() {
		f, problems, err := Parse(builtinEffectsJSON)
		if err != nil {
			// The file is compiled in; a parse failure is a build defect, and
			// hiding it as an empty rule set would be the quiet kind.
			panic(fmt.Sprintf("builtin_effects.json: %v", err))
		}
		for _, p := range problems {
			panic(fmt.Sprintf("builtin_effects.json: %v", p))
		}
		builtinEffects = f.Rules
	})
	return append([]Rule(nil), builtinEffects...)
}

// Set is a merged rule set plus everything that went wrong assembling it.
//
// Problems are carried rather than returned as a fatal error: a bad rule in a
// shared org file must not stop someone reading code, and a silently dropped
// rule is exactly the failure this package exists to avoid. They surface in
// the UI instead.
type Set struct {
	Rules    []Rule
	Problems []string
	// Sources records which file each rule id came from, so "why is this edge
	// here" is answerable down to the file someone else committed.
	Sources map[string]string
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

	// The shipped effect rules go in first, so every configured tier can
	// override or switch one off by naming its id — the same way a Go
	// built-in is addressed, without the body having to be Go.
	for _, r := range BuiltinEffects() {
		byID[r.ID] = len(set.Rules)
		set.Rules = append(set.Rules, r)
		set.Sources[r.ID] = BuiltinSource
	}

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
