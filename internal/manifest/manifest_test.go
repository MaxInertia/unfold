package manifest

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReadMissingIsNotAnError(t *testing.T) {
	// Most repos unfold opens have no manifest; that's the normal case, not
	// a failure.
	m, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil manifest for a directory without one, got %+v", m)
	}
}

func TestRead(t *testing.T) {
	m, err := Read("testdata/svc")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m == nil {
		t.Fatal("expected a manifest")
	}
	if m.Name != "conversation" {
		t.Errorf("name: got %q, want conversation", m.Name)
	}
	want := []string{"/v1/conversations", "/v1/health"}
	if !reflect.DeepEqual(m.PublicRoutes, want) {
		t.Errorf("publicRoutes: got %v, want %v", m.PublicRoutes, want)
	}
}

// The two fields can sit on the same list entry or on different ones, so
// both are collected across the whole list rather than paired positionally.
// An excluded proto is still implemented here — it's just not callable from
// other services — so it belongs in the surface.
func TestIncludedProtos(t *testing.T) {
	m, err := Read("testdata/svc")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	paths, excluded := m.IncludedProtos()
	want := []string{
		"conversation/v1/api.proto",
		"conversation/v1/admin.proto",
		"conversation/v1/internal.proto",
		"conversation/v1/private.proto",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths: got %v, want %v", paths, want)
	}
	// excludeFromSdk as a path names a different file.
	if !excluded["conversation/v1/internal.proto"] {
		t.Error("internal.proto should be marked excluded from the SDK")
	}
	// excludeFromSdk: true excludes the entry's own path.
	if !excluded["conversation/v1/private.proto"] {
		t.Error("private.proto should be excluded by its own boolean flag")
	}
	// excludeFromSdk: false excludes nothing — and must never be read as a
	// filename, which is what a strict string decode would do.
	if excluded["conversation/v1/api.proto"] {
		t.Error("api.proto should not be marked excluded")
	}
	for _, p := range paths {
		if p == "false" || p == "true" {
			t.Errorf("a boolean flag leaked into the proto path list: %v", paths)
		}
	}
}

// The key appears in real manifests as a boolean flag and as a path, so both
// have to decode. A strict string field turns `excludeFromSdk: false` into
// the filename "false" and then fails to open it.
func TestExcludeFlagAcceptsBoolAndPath(t *testing.T) {
	tests := []struct {
		yaml string
		want ExcludeFlag
	}{
		{"excludeFromSdk: true", ExcludeFlag{Flag: true}},
		{"excludeFromSdk: false", ExcludeFlag{}},
		{"excludeFromSdk: a/v1/x.proto", ExcludeFlag{Path: "a/v1/x.proto"}},
		{`excludeFromSdk: "true"`, ExcludeFlag{Flag: true}},
		{"excludeFromSdk:", ExcludeFlag{}},
		{"excludeFromSdk: [1,2]", ExcludeFlag{}}, // unknown shape, ignored
	}
	for _, tt := range tests {
		var got struct {
			ExcludeFromSdk ExcludeFlag `yaml:"excludeFromSdk"`
		}
		if err := yaml.Unmarshal([]byte(tt.yaml), &got); err != nil {
			t.Fatalf("%s: %v", tt.yaml, err)
		}
		if got.ExcludeFromSdk != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.yaml, got.ExcludeFromSdk, tt.want)
		}
	}
}

func TestIncludedProtosNilManifest(t *testing.T) {
	var m *Manifest
	paths, excluded := m.IncludedProtos()
	if len(paths) != 0 || len(excluded) != 0 {
		t.Errorf("nil manifest should yield nothing, got %v / %v", paths, excluded)
	}
}
