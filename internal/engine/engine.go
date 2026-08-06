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

// Load constructs the engine for lang and loads the project rooted at dir.
// target is the engine-specific scope (a Go package pattern like "./..."
// for Go; ignored by the TS engine, which loads the whole tsconfig project).
func Load(lang Lang, dir, target string) (model.Engine, error) {
	switch lang {
	case LangGo:
		if Workspace != "" {
			dirs, err := workspace.Discover(Workspace)
			if err != nil {
				return nil, err
			}
			return workspace.Open(dirs, projectDir(dir), ProtoRoot, IndexMode)
		}
		idx := indexer.New()
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
