// Package engine selects and constructs the right indexing engine for a
// project. It is the one place that knows about every concrete engine, so
// main and the server stay language-agnostic (they only see model.Engine).
package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
	"github.com/MaxInertia/unfold/internal/tsengine"
	"github.com/MaxInertia/unfold/internal/workspace"
)

// Lang names a supported engine language.
type Lang string

const (
	LangGo Lang = "go"
	LangTS Lang = "typescript"
)

// Detect chooses an engine language for dir. An explicit lang ("go" /
// "typescript" / "ts") overrides detection; an empty lang autodetects from
// marker files (go.mod → Go, tsconfig.json/package.json → TypeScript),
// defaulting to Go for back-compatibility with bare "./..." invocations.
func Detect(dir, lang string) (Lang, error) {
	switch lang {
	case "go":
		return LangGo, nil
	case "ts", "typescript":
		return LangTS, nil
	case "":
		// autodetect below
	default:
		return "", fmt.Errorf("unknown --lang %q (want go|typescript)", lang)
	}

	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	if fileExists(filepath.Join(dir, "go.mod")) {
		return LangGo, nil
	}
	if fileExists(filepath.Join(dir, "tsconfig.json")) || fileExists(filepath.Join(dir, "package.json")) {
		return LangTS, nil
	}
	return LangGo, nil
}

// ProtoRoot is the shared proto repository that a microservice.yaml's
// protoPaths resolve against. Those paths are relative to that repo rather
// than to the service, so unfold can't find it on its own — the user points
// at it with --proto-root. Package-level because it's a process-wide setting
// that must survive the engine rebuilds watch mode performs.
var ProtoRoot string

// Workspace, when set, is a directory of sibling repository checkouts. Every
// module under it is opened together so a cross-service call can be followed
// into the repo that implements it. IndexMode selects when each repo's Go
// code is built (see workspace.Mode).
var (
	Workspace string
	IndexMode = workspace.ModeAuto
)

// LinkedRepos are repositories added after launch, from the UI. A workspace
// discovered under --workspace is a directory of checkouts; these need not be
// siblings of anything, which is the point — you find out mid-session that
// the call you're following lands in a repo you didn't open.
//
// Package-level for the same reason ProtoRoot is: it has to survive the
// engine rebuilds watch mode performs, and it is what turns a single-repo
// session into a workspace on the next load.
var LinkedRepos []string

// RecognizerFiles are the rule files to load, in precedence order — later
// files win. Process-wide for the same reason ProtoRoot is: it must survive
// the engine rebuilds watch mode performs.
var RecognizerFiles []string

// LinkRepo adds a repository to the set opened on the next load, reporting
// whether it was new. Nothing is re-indexed here — the caller reloads the
// engine, which is the same path watch mode uses and the only one that
// rebuilds the cross-repo declaration join consistently.
func LinkRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for _, d := range LinkedRepos {
		if d == abs {
			return false
		}
	}
	LinkedRepos = append(LinkedRepos, abs)
	return true
}

// UnlinkRepo removes a linked repository, reporting whether it was present.
func UnlinkRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for i, d := range LinkedRepos {
		if d == abs {
			LinkedRepos = append(LinkedRepos[:i], LinkedRepos[i+1:]...)
			return true
		}
	}
	return false
}

// goDirs is the full set of repositories to open: whatever --workspace
// discovers, plus anything linked from the UI, plus the module the session
// was launched in — which has to be in the set explicitly, because linking a
// repo from a plain single-repo session is exactly the case where nothing
// else names it.
//
// Returns nil when there is only the launch module and nothing linked, which
// is the signal to stay a plain single-repo index rather than paying for
// workspace machinery to federate one thing. That signal is the reason a
// discovery failure must be an error and not an empty result: the two are
// indistinguishable downstream, and the quiet one comes up as a session with
// no platform level, no zoom-out, and nothing said about why.
func goDirs(dir string) ([]string, error) {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		abs, err := filepath.Abs(d)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		dirs = append(dirs, abs)
	}

	if Workspace != "" {
		found, err := workspace.Discover(Workspace)
		if err != nil {
			return nil, fmt.Errorf("--workspace: %w", err)
		}
		for _, d := range found {
			add(d)
		}
	}
	if len(LinkedRepos) > 0 {
		add(moduleRoot(projectDir(dir)))
		for _, d := range LinkedRepos {
			add(d)
		}
	}
	if Workspace == "" && len(LinkedRepos) == 0 {
		return nil, nil
	}
	return dirs, nil
}

// moduleRoot walks up from dir to the directory holding its go.mod. unfold is
// routinely started from a package inside a module, and a workspace's repos
// are module roots — handing it a subdirectory would make the launch repo
// look like a different, nameless one.
func moduleRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return dir // no go.mod anywhere above; leave it alone
		}
		abs = parent
	}
}

// Load constructs the engine for lang and loads the project rooted at dir.
// target is the engine-specific scope (a Go package pattern like "./..."
// for Go; ignored by the TS engine, which loads the whole tsconfig project).
func Load(lang Lang, dir, target string) (model.Engine, error) {
	switch lang {
	case LangGo:
		dirs, err := goDirs(dir)
		if err != nil {
			return nil, err
		}
		if len(dirs) > 0 {
			workspace.RulePaths = RecognizerFiles
			// A repo you linked by hand is one you mean to walk into, so it is
			// indexed behind the primary even when the workspace is too large
			// to be eager — which is the state linking one more repo can
			// itself produce.
			workspace.Preload = append([]string(nil), LinkedRepos...)
			return workspace.Open(dirs, projectDir(dir), ProtoRoot, IndexMode)
		}
		idx := indexer.New()
		idx.SetRules(rules.Load(RecognizerFiles...))
		_ = idx.SetProtoRoot(ProtoRoot)
		if err := idx.Load(dir, target); err != nil {
			return nil, err
		}
		return idx, nil
	case LangTS:
		return tsengine.Load(dir, target)
	default:
		return nil, fmt.Errorf("unsupported language %q", lang)
	}
}

// projectDir resolves an empty --dir to the working directory, so the repo
// the user is standing in becomes the workspace's primary.
func projectDir(dir string) string {
	if dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
