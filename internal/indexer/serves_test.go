package indexer

import (
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

func loadServedWith(t *testing.T, set rules.Set) *Indexer {
	t.Helper()
	dir, err := filepath.Abs("testdata/served")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	idx.SetRules(set)
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return idx
}

// The general declared tier: a key stated in a rules file is surface, whatever
// kind it is and whether or not any code here shows it. This is what the
// manifest's protoPaths do for one organisation's gRPC, for everyone else and
// for every other kind of key.
func TestAServesEntryIsSurface(t *testing.T) {
	idx := loadServedWith(t, rules.Set{Serves: []rules.Serve{
		{Service: "served", Kind: "http.route", Keys: []string{"GET /v1/notes"}, From: "/org/recognizers.json"},
	}})
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	b := findBinding(sv.Inbound, "http.route", "GET /v1/notes")
	if b == nil {
		t.Fatalf("a declared route should be inbound surface; got %+v", sv.Inbound)
	}
	if b.Confidence != model.ConfDeclared {
		t.Errorf("a stated fact is declared, got %q", b.Confidence)
	}
	if b.Detail == "" {
		t.Errorf("the row should say where the declaration came from")
	}
}

// A declared gRPC method still opens as its implementation, so declaring one
// is not a step down from having the code read.
func TestADeclaredRPCLinksToItsImplementation(t *testing.T) {
	idx := loadServedWith(t, rules.Set{
		Rules:  []rules.Rule{{ID: servedRuleID, Enabled: new(bool)}},
		Serves: []rules.Serve{{Service: "served", Kind: "grpc.method", Keys: []string{"notes.v1.NotesService/AddNote"}}},
	})
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	b := findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/AddNote")
	if b == nil {
		t.Fatalf("the declared RPC should be surface; got %+v", sv.Inbound)
	}
	if b.Target != "(*example.com/served.NotesServer).AddNote" {
		t.Errorf("a declared RPC should still open as its implementation, got %q", b.Target)
	}
}

// What the code shows describes itself. A declaration that repeats it adds
// nothing and would put the same RPC on the card twice — and a pattern that
// covers it would put "the whole service" beside the methods it stands for.
func TestCodeWinsOverADeclarationOfTheSameSurface(t *testing.T) {
	idx := loadServedWith(t, rules.Set{Serves: []rules.Serve{
		{Service: "served", Kind: "grpc.method", Keys: []string{"notes.v1.NotesService/*"}},
	}})
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	for _, b := range sv.Inbound {
		if b.Key == "notes.v1.NotesService/*" {
			t.Errorf("a pattern the code already covers should not be a row of its own: %+v", b)
		}
	}
	add := findBinding(sv.Inbound, "grpc.method", "notes.v1.NotesService/AddNote")
	if add == nil || add.Confidence != model.ConfExact {
		t.Errorf("the registration should still describe the RPC, got %+v", add)
	}
}

// An entry about another service says nothing about this one.
func TestAServesEntryForAnotherServiceIsNotThisSurface(t *testing.T) {
	idx := loadServedWith(t, rules.Set{Serves: []rules.Serve{
		{Service: "billing", Kind: "http.route", Keys: []string{"GET /v1/invoices"}},
	}})
	sv, err := idx.ServiceView("")
	if err != nil {
		t.Fatalf("ServiceView: %v", err)
	}
	if b := findBinding(sv.Inbound, "http.route", "GET /v1/invoices"); b != nil {
		t.Errorf("another service's declaration must not appear here, got %+v", b)
	}
}
