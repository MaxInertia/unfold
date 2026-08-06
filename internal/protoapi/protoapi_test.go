package protoapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	methods, err := Load("testdata/protoroot",
		[]string{"conversation/v1/api.proto", "conversation/v1/internal.proto"},
		map[string]bool{"conversation/v1/internal.proto": true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("expected 3 methods, got %d: %+v", len(methods), methods)
	}

	byName := map[string]Method{}
	for _, m := range methods {
		byName[m.FullName] = m
	}

	// The full name is the join key: it's what the generated SDK on the
	// calling side names too.
	get, ok := byName["conversation.v1.ConversationService/GetConversation"]
	if !ok {
		t.Fatalf("missing GetConversation; got %v", keys(byName))
	}
	if get.Service != "conversation.v1.ConversationService" || get.Name != "GetConversation" {
		t.Errorf("service/name: got %q / %q", get.Service, get.Name)
	}
	if get.ExcludedFromSDK {
		t.Error("api.proto is not excluded from the SDK")
	}
	if get.ClientStreaming || get.ServerStreaming {
		t.Error("GetConversation is unary")
	}

	stream := byName["conversation.v1.ConversationService/StreamConversation"]
	if !stream.ServerStreaming || stream.ClientStreaming {
		t.Errorf("StreamConversation should be server-streaming only: %+v", stream)
	}

	// Excluded protos still contribute surface — the service implements
	// them, other services just can't call them.
	purge, ok := byName["conversation.v1.MaintenanceService/Purge"]
	if !ok {
		t.Fatalf("missing Purge; got %v", keys(byName))
	}
	if !purge.ExcludedFromSDK {
		t.Error("internal.proto methods should be marked excluded from the SDK")
	}
}

// Sorting keeps the surface stable between loads.
func TestLoadIsSorted(t *testing.T) {
	methods, err := Load("testdata/protoroot",
		[]string{"conversation/v1/internal.proto", "conversation/v1/api.proto"}, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 1; i < len(methods); i++ {
		if methods[i-1].FullName > methods[i].FullName {
			t.Fatalf("not sorted: %q before %q", methods[i-1].FullName, methods[i].FullName)
		}
	}
}

// A wrong proto root must report an error rather than quietly returning an
// empty surface, which would read as "this service declares no RPCs".
func TestLoadBadRootErrors(t *testing.T) {
	got, err := Load(t.TempDir(), []string{"conversation/v1/api.proto"}, nil)
	if err == nil {
		t.Fatal("expected an error for a proto root that doesn't contain the paths")
	}
	if got != nil {
		t.Errorf("nothing parsed, so no methods should come back; got %+v", got)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("error should name the failing step, got %v", err)
	}
}

// Imports are not resolved, because only names are wanted and the import
// closure routinely reaches outside the proto repository (googleapis'
// google/rpc/*.proto and friends). A proto importing something absent must
// still yield its services.
func TestLoadIgnoresUnresolvableImports(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "billing", "v1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `syntax = "proto3";
package billing.v1;
import "google/rpc/code.proto";
import "google/api/annotations.proto";

message Req {}
message Res {}

service BillingService {
  rpc Charge(Req) returns (Res);
}
`
	if err := os.WriteFile(filepath.Join(dir, "api.proto"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root, []string{"billing/v1/api.proto"}, nil)
	if err != nil {
		t.Fatalf("unresolvable imports should not fail a surface listing: %v", err)
	}
	if len(got) != 1 || got[0].FullName != "billing.v1.BillingService/Charge" {
		t.Fatalf("got %+v, want one billing.v1.BillingService/Charge", got)
	}
}

// One unreadable file must not hide the surface the others declare.
func TestLoadIsPartial(t *testing.T) {
	got, err := Load("testdata/protoroot",
		[]string{"conversation/v1/api.proto", "conversation/v1/missing.proto"}, nil)
	if err == nil {
		t.Fatal("expected the missing file to be reported")
	}
	if len(got) != 2 {
		t.Fatalf("expected the readable file's methods to survive, got %+v", got)
	}
	if !strings.Contains(err.Error(), "missing.proto") {
		t.Errorf("error should name the file that failed, got %v", err)
	}
}

func TestLoadNoPathsOrRoot(t *testing.T) {
	for _, tc := range []struct {
		root  string
		paths []string
	}{
		{"", []string{"a.proto"}},
		{"testdata/protoroot", nil},
	} {
		m, err := Load(tc.root, tc.paths, nil)
		if err != nil || m != nil {
			t.Errorf("Load(%q, %v) = (%v, %v), want (nil, nil)", tc.root, tc.paths, m, err)
		}
	}
}

func keys(m map[string]Method) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
