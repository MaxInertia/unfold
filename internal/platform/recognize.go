// Package platform turns ordinary call sites into model.Bindings — the
// key-joined edges that connect one service's code to the rest of a platform.
//
// A publish and its subscriber share no AST edge; they share a *string*. So
// recognizers don't resolve anything, they extract keys: which topic, which
// route, which URL. Joining the two ends of an edge happens later, above a
// multi-repo index, using the keys collected here.
//
// A recognizer sees a neutral Call — package path, receiver, function name,
// constant-folded arguments — and never an AST. That keeps the rules
// independent of the Go indexer, so a second language engine can feed the
// same set, and keeps each rule small enough to test on its own.
package platform

import (
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
)

// Arg is one argument at a call site, reduced to the two things a recognizer
// can use: its constant string value (Go folds `"POST " + routeConst` for us,
// so this covers more than bare literals) and, when the argument is a
// function value, the target it names — that's how a route registration finds
// its handler.
type Arg struct {
	Value  string
	Known  bool
	Target model.TargetID
}

// Call is one call site, stripped of syntax.
type Call struct {
	// PkgPath is the callee's package ("net/http").
	PkgPath string
	// Recv is the receiver's bare type name ("ServeMux"), empty for a
	// package-level function; RecvPkg is that type's package.
	Recv    string
	RecvPkg string
	// Func is the callee's name ("HandleFunc").
	Func string
	Args []Arg

	// Callee is the resolved target of the call, when there is one. Used to
	// attribute a transitively-discovered edge to the right call site.
	Callee model.TargetID

	// CalleeInvoke is the gRPC method path the callee ultimately invokes
	// ("/pkg.Service/Method"), when it does. Filled by the engine from the
	// callee's own body, which is where generated clients state it.
	CalleeInvoke string

	// Site is the enclosing function, and File/Line locate the call.
	Site model.TargetID
	File string
	Line int
}

// A Recognizer returns the bindings a call site implies, or nil.
type Recognizer func(Call) []model.Binding

// Recognizers is the active rule set. Which rules *should* be active is
// ultimately a per-project question (detected from go.mod / package.json —
// there's no point running Kafka rules against a repo that doesn't import
// it), but with a handful of stdlib-and-GCP rules the cost of running them
// all is a few string comparisons per call site.
var Recognizers = []Recognizer{
	HTTPRoutes,
	PubSub,
	HTTPClientCalls,
	GRPCClientCalls,
}

// Extract runs every recognizer over one call site.
func Extract(c Call) []model.Binding {
	var out []model.Binding
	for _, r := range Recognizers {
		out = append(out, r(c)...)
	}
	return out
}

const netHTTP = "net/http"

// HTTPRoutes recognizes net/http route registration — the inbound surface of
// most Go services. It covers both `mux.HandleFunc(...)` on a *ServeMux and
// the package-level `http.HandleFunc(...)` against DefaultServeMux.
//
// The pattern string is the join key, and it is deliberately kept verbatim
// (including Go 1.22's "METHOD /path" form) because the *client* side of the
// edge — an outbound call in another repo — will produce the same shape.
func HTTPRoutes(c Call) []model.Binding {
	if c.Func != "HandleFunc" && c.Func != "Handle" {
		return nil
	}
	onMux := c.RecvPkg == netHTTP && c.Recv == "ServeMux"
	onPkg := c.Recv == "" && c.PkgPath == netHTTP
	if !onMux && !onPkg {
		return nil
	}
	if len(c.Args) == 0 || !c.Args[0].Known || c.Args[0].Value == "" {
		return nil
	}
	b := model.Binding{
		Role:       model.RoleInbound,
		Kind:       "http.route",
		Key:        normalizeRoute(c.Args[0].Value),
		Detail:     callLabel(c),
		Site:       c.Site,
		File:       c.File,
		Line:       c.Line,
		Confidence: model.ConfExact,
	}
	// The handler is the second argument. Naming it is what makes a route
	// clickable straight into its implementation.
	if len(c.Args) > 1 {
		b.Target = c.Args[1].Target
	}
	return []model.Binding{b}
}

// PubSub recognizes GCP Pub/Sub topic and subscription handles.
//
// The name is taken where it's *stated* — `client.Topic("orders-v1")` — not
// where the publish happens, because following the handle to its .Publish
// call is a dataflow problem and the name is the part that joins the edge.
// The cost is that direction is a guess: a topic handle usually means
// publishing but can be admin code, so topics are marked inferred while
// subscriptions (which only ever receive) stay exact.
func PubSub(c Call) []model.Binding {
	const pkg = "cloud.google.com/go/pubsub"
	if c.RecvPkg != pkg || c.Recv != "Client" {
		return nil
	}
	var (
		kind       string
		role       model.BindingRole
		confidence model.BindingConfidence
		argIdx     int
	)
	switch c.Func {
	case "Topic", "TopicInProject":
		kind, role, confidence, argIdx = "pubsub.topic", model.RoleOutbound, model.ConfInferred, 0
	case "CreateTopic", "CreateTopicWithConfig":
		// CreateTopic(ctx, id) — the id is the second argument.
		kind, role, confidence, argIdx = "pubsub.topic", model.RoleOutbound, model.ConfInferred, 1
	case "Subscription", "SubscriptionInProject":
		kind, role, confidence, argIdx = "pubsub.subscription", model.RoleInbound, model.ConfExact, 0
	case "CreateSubscription":
		kind, role, confidence, argIdx = "pubsub.subscription", model.RoleInbound, model.ConfExact, 1
	default:
		return nil
	}
	if len(c.Args) <= argIdx || !c.Args[argIdx].Known || c.Args[argIdx].Value == "" {
		return nil
	}
	return []model.Binding{{
		Role:       role,
		Kind:       kind,
		Key:        c.Args[argIdx].Value,
		Detail:     callLabel(c),
		Site:       c.Site,
		File:       c.File,
		Line:       c.Line,
		Confidence: confidence,
	}}
}

// HTTPClientCalls recognizes outbound net/http calls whose URL is statically
// known. A URL built from config at runtime is skipped rather than guessed —
// the design's rule is that resolving the *host* is the wrong problem anyway.
// What joins this to another service is the method and path, which is why the
// key drops the scheme and host.
func HTTPClientCalls(c Call) []model.Binding {
	var method string
	switch c.Func {
	case "Get":
		method = "GET"
	case "Post", "PostForm":
		method = "POST"
	case "Head":
		method = "HEAD"
	default:
		return nil
	}
	onClient := c.RecvPkg == netHTTP && c.Recv == "Client"
	onPkg := c.Recv == "" && c.PkgPath == netHTTP
	if !onClient && !onPkg {
		return nil
	}
	if len(c.Args) == 0 || !c.Args[0].Known || c.Args[0].Value == "" {
		return nil
	}
	path, ok := urlPath(c.Args[0].Value)
	if !ok {
		return nil
	}
	return []model.Binding{{
		Role:       model.RoleOutbound,
		Kind:       "http.call",
		Key:        method + " " + path,
		Detail:     callLabel(c) + " " + c.Args[0].Value,
		Site:       c.Site,
		File:       c.File,
		Line:       c.Line,
		Confidence: model.ConfExact,
	}}
}

// GRPCClientCalls recognizes an outbound call through a generated gRPC
// client. The key comes from the callee's own body rather than from the call
// site, so it's exact: the same string the serving repo's proto declares.
// That's what lets an outbound edge join to an implementation in another repo
// without inferring anything about client types or hostnames.
func GRPCClientCalls(c Call) []model.Binding {
	if c.CalleeInvoke == "" {
		return nil
	}
	key := strings.TrimPrefix(c.CalleeInvoke, "/")
	svc, method, ok := strings.Cut(key, "/")
	if !ok || svc == "" || method == "" {
		return nil
	}
	return []model.Binding{{
		Role:       model.RoleOutbound,
		Kind:       "grpc.method",
		Key:        svc + "/" + method,
		Detail:     callLabel(c),
		Site:       c.Site,
		File:       c.File,
		Line:       c.Line,
		Confidence: model.ConfExact,
	}}
}

// normalizeRoute trims a Go 1.22 pattern to a stable "METHOD /path" or
// "/path" key, dropping the optional host so a registration and a client call
// produce comparable keys.
func normalizeRoute(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	method, rest, found := strings.Cut(pattern, " ")
	if !found || !isMethod(method) {
		method, rest = "", pattern
	}
	rest = strings.TrimSpace(rest)
	// "example.com/v1/orders" — a host prefix, not a path.
	if !strings.HasPrefix(rest, "/") {
		if _, after, ok := strings.Cut(rest, "/"); ok {
			rest = "/" + after
		}
	}
	if method == "" {
		return rest
	}
	return method + " " + rest
}

func isMethod(s string) bool {
	switch s {
	case "GET", "PUT", "POST", "HEAD", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}

// urlPath reduces a URL to its path, so the key matches what the serving side
// registered. Reported false when there's no usable path (a bare host, or a
// template whose path half wasn't constant).
func urlPath(raw string) (string, bool) {
	rest := raw
	if _, after, ok := strings.Cut(rest, "://"); ok {
		rest = after
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false
		}
		rest = "/" + after
	}
	if !strings.HasPrefix(rest, "/") {
		return "", false
	}
	// Query strings aren't part of the route key.
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// callLabel renders the call the way it reads in source, for provenance.
func callLabel(c Call) string {
	if c.Recv != "" {
		return c.Recv + "." + c.Func
	}
	return shortPkg(c.PkgPath) + "." + c.Func
}

func shortPkg(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
