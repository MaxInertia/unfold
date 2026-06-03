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

// Load constructs the engine for lang and loads the project rooted at dir.
// target is the engine-specific scope (a Go package pattern like "./..."
// for Go; ignored by the TS engine, which loads the whole tsconfig project).
func Load(lang Lang, dir, target string) (model.Engine, error) {
	switch lang {
	case LangGo:
		idx := indexer.New()
		if err := idx.Load(dir, target); err != nil {
			return nil, err
		}
		return idx, nil
	case LangTS:
		return nil, fmt.Errorf("TypeScript support is not wired yet")
	default:
		return nil, fmt.Errorf("unsupported language %q", lang)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
