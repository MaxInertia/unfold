package manifest

import (
	"reflect"
	"testing"
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
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths: got %v, want %v", paths, want)
	}
	if !excluded["conversation/v1/internal.proto"] {
		t.Error("internal.proto should be marked excluded from the SDK")
	}
	if excluded["conversation/v1/api.proto"] {
		t.Error("api.proto should not be marked excluded")
	}
}

func TestIncludedProtosNilManifest(t *testing.T) {
	var m *Manifest
	paths, excluded := m.IncludedProtos()
	if len(paths) != 0 || len(excluded) != 0 {
		t.Errorf("nil manifest should yield nothing, got %v / %v", paths, excluded)
	}
}
