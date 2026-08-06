package platform

import (
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
)

func lit(s string) Arg { return Arg{Value: s, Known: true} }

func TestHTTPRoutes(t *testing.T) {
	tests := []struct {
		name string
		call Call
		want *model.Binding
	}{
		{
			name: "mux.HandleFunc with handler",
			call: Call{
				PkgPath: netHTTP, Recv: "ServeMux", RecvPkg: netHTTP, Func: "HandleFunc",
				Args: []Arg{lit("/api/health"), {Target: "(*pkg.Server).handleHealth"}},
				Site: "(*pkg.Server).Handler",
			},
			want: &model.Binding{
				Role: model.RoleInbound, Kind: "http.route", Key: "/api/health",
				Target: "(*pkg.Server).handleHealth", Site: "(*pkg.Server).Handler",
				Detail: "ServeMux.HandleFunc", Confidence: model.ConfExact,
			},
		},
		{
			name: "package-level http.Handle on DefaultServeMux",
			call: Call{
				PkgPath: netHTTP, Func: "Handle",
				Args: []Arg{lit("POST /v1/orders"), {Target: "pkg.orders"}},
			},
			want: &model.Binding{
				Role: model.RoleInbound, Kind: "http.route", Key: "POST /v1/orders",
				Target: "pkg.orders", Detail: "http.Handle", Confidence: model.ConfExact,
			},
		},
		{
			name: "host-qualified pattern keeps only the path",
			call: Call{
				PkgPath: netHTTP, Recv: "ServeMux", RecvPkg: netHTTP, Func: "HandleFunc",
				Args: []Arg{lit("GET example.com/v1/orders")},
			},
			want: &model.Binding{
				Role: model.RoleInbound, Kind: "http.route", Key: "GET /v1/orders",
				Detail: "ServeMux.HandleFunc", Confidence: model.ConfExact,
			},
		},
		{
			// A pattern assembled at runtime has no key to join on, and
			// guessing one would be worse than omitting the binding.
			name: "non-constant pattern is skipped",
			call: Call{
				PkgPath: netHTTP, Recv: "ServeMux", RecvPkg: netHTTP, Func: "HandleFunc",
				Args: []Arg{{}, {Target: "pkg.h"}},
			},
		},
		{
			name: "HandleFunc on an unrelated type is not a route",
			call: Call{
				PkgPath: "github.com/acme/router", Recv: "Router", RecvPkg: "github.com/acme/router",
				Func: "HandleFunc", Args: []Arg{lit("/x")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HTTPRoutes(tt.call)
			assertBinding(t, got, tt.want)
		})
	}
}

func TestPubSub(t *testing.T) {
	const pkg = "cloud.google.com/go/pubsub"
	tests := []struct {
		name string
		call Call
		want *model.Binding
	}{
		{
			// A topic handle is only evidence the code names the topic —
			// whether it publishes is a dataflow question — so it's inferred.
			name: "Client.Topic is an outbound topic, inferred",
			call: Call{
				PkgPath: pkg, Recv: "Client", RecvPkg: pkg, Func: "Topic",
				Args: []Arg{lit("orders-v1")}, Site: "pkg.publish",
			},
			want: &model.Binding{
				Role: model.RoleOutbound, Kind: "pubsub.topic", Key: "orders-v1",
				Site: "pkg.publish", Detail: "Client.Topic", Confidence: model.ConfInferred,
			},
		},
		{
			name: "Client.Subscription is inbound and exact",
			call: Call{
				PkgPath: pkg, Recv: "Client", RecvPkg: pkg, Func: "Subscription",
				Args: []Arg{lit("orders-worker")},
			},
			want: &model.Binding{
				Role: model.RoleInbound, Kind: "pubsub.subscription", Key: "orders-worker",
				Detail: "Client.Subscription", Confidence: model.ConfExact,
			},
		},
		{
			// CreateTopic takes a context first, so the id is the second arg.
			name: "CreateTopic reads the id past the context",
			call: Call{
				PkgPath: pkg, Recv: "Client", RecvPkg: pkg, Func: "CreateTopic",
				Args: []Arg{{}, lit("orders-v1")},
			},
			want: &model.Binding{
				Role: model.RoleOutbound, Kind: "pubsub.topic", Key: "orders-v1",
				Detail: "Client.CreateTopic", Confidence: model.ConfInferred,
			},
		},
		{
			name: "CreateSubscription reads the id past the context",
			call: Call{
				PkgPath: pkg, Recv: "Client", RecvPkg: pkg, Func: "CreateSubscription",
				Args: []Arg{{}, lit("orders-worker"), {}},
			},
			want: &model.Binding{
				Role: model.RoleInbound, Kind: "pubsub.subscription", Key: "orders-worker",
				Detail: "Client.CreateSubscription", Confidence: model.ConfExact,
			},
		},
		{
			name: "a same-named method on another package is ignored",
			call: Call{
				PkgPath: "github.com/acme/queue", Recv: "Client", RecvPkg: "github.com/acme/queue",
				Func: "Topic", Args: []Arg{lit("orders-v1")},
			},
		},
		{
			name: "non-constant topic id is skipped",
			call: Call{
				PkgPath: pkg, Recv: "Client", RecvPkg: pkg, Func: "Topic",
				Args: []Arg{{}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBinding(t, PubSub(tt.call), tt.want)
		})
	}
}

func TestHTTPClientCalls(t *testing.T) {
	tests := []struct {
		name string
		call Call
		want *model.Binding
	}{
		{
			// The key drops scheme and host: joining to the serving side
			// happens on method+path, never on a hostname.
			name: "absolute URL reduces to method and path",
			call: Call{
				PkgPath: netHTTP, Func: "Get",
				Args: []Arg{lit("https://orders.internal/v1/orders")}, Site: "pkg.fetch",
			},
			want: &model.Binding{
				Role: model.RoleOutbound, Kind: "http.call", Key: "GET /v1/orders",
				Site: "pkg.fetch", Confidence: model.ConfExact,
			},
		},
		{
			name: "client.Post on *http.Client",
			call: Call{
				PkgPath: netHTTP, Recv: "Client", RecvPkg: netHTTP, Func: "Post",
				Args: []Arg{lit("https://orders.internal/v1/orders?sync=1")},
			},
			want: &model.Binding{
				Role: model.RoleOutbound, Kind: "http.call", Key: "POST /v1/orders",
				Confidence: model.ConfExact,
			},
		},
		{
			name: "URL with no path is skipped",
			call: Call{
				PkgPath: netHTTP, Func: "Get", Args: []Arg{lit("https://orders.internal")},
			},
		},
		{
			name: "runtime-built URL is skipped",
			call: Call{PkgPath: netHTTP, Func: "Get", Args: []Arg{{}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBinding(t, HTTPClientCalls(tt.call), tt.want)
		})
	}
}

func TestNormalizeRoute(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/api/health", "/api/health"},
		{"POST /v1/orders", "POST /v1/orders"},
		{"  GET   /v1/orders  ", "GET /v1/orders"},
		{"example.com/v1/orders", "/v1/orders"},
		{"DELETE example.com/v1/orders/{id}", "DELETE /v1/orders/{id}"},
		// "SUBSCRIBE" isn't an HTTP method, so the whole string is the path
		// half — don't silently eat a segment that wasn't a verb.
		{"SUBSCRIBE /x", "/x"},
	}
	for _, tt := range tests {
		if got := normalizeRoute(tt.in); got != tt.want {
			t.Errorf("normalizeRoute(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestURLPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://host/v1/orders", "/v1/orders", true},
		{"http://host/v1/orders?x=1", "/v1/orders", true},
		{"/v1/orders", "/v1/orders", true},
		{"https://host", "", false},
		{"orders.internal", "", false},
	}
	for _, tt := range tests {
		got, ok := urlPath(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("urlPath(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestExtractRunsEveryRecognizer guards the wiring: a call that one rule
// claims must survive the others returning nil.
func TestExtractRunsEveryRecognizer(t *testing.T) {
	got := Extract(Call{
		PkgPath: netHTTP, Recv: "ServeMux", RecvPkg: netHTTP, Func: "HandleFunc",
		Args: []Arg{lit("/api/health")},
	})
	if len(got) != 1 || got[0].Kind != "http.route" {
		t.Fatalf("Extract: got %+v, want one http.route binding", got)
	}
}

// assertBinding compares the fields a recognizer is responsible for. A nil
// want means the call should produce nothing.
func assertBinding(t *testing.T, got []model.Binding, want *model.Binding) {
	t.Helper()
	if want == nil {
		if len(got) != 0 {
			t.Fatalf("expected no bindings, got %+v", got)
		}
		return
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one binding, got %d: %+v", len(got), got)
	}
	g := got[0]
	if g.Role != want.Role {
		t.Errorf("role: got %q, want %q", g.Role, want.Role)
	}
	if g.Kind != want.Kind {
		t.Errorf("kind: got %q, want %q", g.Kind, want.Kind)
	}
	if g.Key != want.Key {
		t.Errorf("key: got %q, want %q", g.Key, want.Key)
	}
	if g.Target != want.Target {
		t.Errorf("target: got %q, want %q", g.Target, want.Target)
	}
	if g.Site != want.Site {
		t.Errorf("site: got %q, want %q", g.Site, want.Site)
	}
	if g.Confidence != want.Confidence {
		t.Errorf("confidence: got %q, want %q", g.Confidence, want.Confidence)
	}
	if want.Detail != "" && g.Detail != want.Detail {
		t.Errorf("detail: got %q, want %q", g.Detail, want.Detail)
	}
}

// The method-path shape has to be tight enough that an HTTP route registered
// with a literal isn't read as a gRPC call: "/api/health" has two slashes and
// two identifiers too.
func TestIsMethodPath(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"/accountgroup.v1.AccountGroupService/GetMulti", true},
		{"/conversation.v1.ConversationService/Get", true},
		{"/Service/Method", true}, // proto with no package
		{"/api/health", false},
		{"/v1/orders", false},
		{"/api/orders/list", false},
		{"POST /v1/orders", false},
		{"/pkg.Service/lowercase", false},
		{"", false},
		{"/", false},
	}
	for _, tt := range tests {
		if got := IsMethodPath(tt.in); got != tt.want {
			t.Errorf("IsMethodPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
