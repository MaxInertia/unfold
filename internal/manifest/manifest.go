// Package manifest reads the microservice.yaml a service declares itself in.
//
// This is the "declared" tier of platform resolution: facts stated by the
// platform rather than recovered from code. Declared facts are stronger than
// anything a recognizer infers — a proto method is the contract both the
// server and its generated SDK are built from — but they can also go stale,
// so the indexer cross-checks them against what it actually found.
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Name is the file every service declares itself in, at its repo root.
const Name = "microservice.yaml"

// ProtoPath is one entry of the protoPaths list. Both fields are proto file
// paths relative to the shared proto repository (not to this service), which
// is why resolving them needs a separately configured proto root.
type ProtoPath struct {
	// Path is a proto file whose services this repo implements.
	Path string `yaml:"path"`
	// ExcludeFromSdk names a proto file left out of SDK generation. Its
	// methods exist but no other service can call them, which is a fact
	// worth showing rather than a gap — and it stops a later "nothing calls
	// this" reading, since nothing *can*.
	ExcludeFromSdk string `yaml:"excludeFromSdk"`
}

// Manifest is the subset of microservice.yaml unfold uses. Unknown keys are
// ignored, so a manifest carrying more than this still parses.
type Manifest struct {
	Name string `yaml:"name"`
	// ProtoPaths accepts both spellings seen in the wild; protoPaths wins
	// when both are present.
	ProtoPaths    []ProtoPath `yaml:"protoPaths"`
	ProtoPathsAlt []ProtoPath `yaml:"protopaths"`
	// PublicRoutes are paths reachable from outside the platform.
	PublicRoutes []string `yaml:"publicRoutes"`
}

type document struct {
	Microservice Manifest `yaml:"microservice"`
}

// Read loads the manifest at dir/microservice.yaml. A missing file is not an
// error — most repos unfold opens won't have one — and yields (nil, nil).
func Read(dir string) (*Manifest, error) {
	path := filepath.Join(dir, Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	m := doc.Microservice
	if len(m.ProtoPaths) == 0 {
		m.ProtoPaths = m.ProtoPathsAlt
	}
	m.ProtoPathsAlt = nil
	return &m, nil
}

// IncludedProtos returns the proto files this service implements, and the set
// of files excluded from SDK generation.
//
// The two fields can appear on the same list entry or on separate ones, so
// both are collected across the whole list rather than paired up positionally.
func (m *Manifest) IncludedProtos() (paths []string, excluded map[string]bool) {
	excluded = map[string]bool{}
	if m == nil {
		return nil, excluded
	}
	seen := map[string]bool{}
	for _, p := range m.ProtoPaths {
		if p.Path != "" && !seen[p.Path] {
			seen[p.Path] = true
			paths = append(paths, p.Path)
		}
		if p.ExcludeFromSdk != "" {
			excluded[p.ExcludeFromSdk] = true
			// An excluded proto is still implemented here — it just isn't
			// callable from other services — so it belongs in the surface.
			if !seen[p.ExcludeFromSdk] {
				seen[p.ExcludeFromSdk] = true
				paths = append(paths, p.ExcludeFromSdk)
			}
		}
	}
	return paths, excluded
}
