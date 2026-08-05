// Package prefs stores the few per-project settings that are chosen in the
// UI rather than passed on the command line, so a choice made once survives
// a restart.
//
// It lives beside the notes store under <project>/.unfold/, and is
// deliberately tiny: anything that belongs in version control belongs in
// microservice.yaml or a flag, not here.
package prefs

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const dirName = ".unfold"
const fileName = "config.json"

// Prefs is the persisted per-project state.
type Prefs struct {
	// ProtoRoot is the shared proto repository that microservice.yaml's
	// protoPaths resolve against. It can't be derived from the project — the
	// paths are relative to a different repo — so it's either a flag or a
	// choice the user makes in the UI.
	ProtoRoot string `json:"protoRoot,omitempty"`
}

func path(projectDir string) string {
	return filepath.Join(projectDir, dirName, fileName)
}

// Load reads the project's prefs. A missing or unreadable file yields zero
// prefs rather than an error: these are conveniences, and failing to start
// over a malformed config would be worse than ignoring it.
func Load(projectDir string) Prefs {
	var p Prefs
	data, err := os.ReadFile(path(projectDir))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return p
		}
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}

// Save writes the project's prefs, creating .unfold/ if needed.
func Save(projectDir string, p Prefs) error {
	if err := os.MkdirAll(filepath.Join(projectDir, dirName), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(projectDir), append(data, '\n'), 0o644)
}
