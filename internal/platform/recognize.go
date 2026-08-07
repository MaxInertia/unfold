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

	// Site is the enclosing function, and File/Line locate the call.
	Site model.TargetID
	File string
	Line int
}

// A Recognizer returns the bindings a call site implies, or nil.
type Recognizer func(Call) []model.Binding

// Builtin is a recognizer written in Go, addressed by a stable id.
//
// The id is what lets a built-in be switched off from configuration the same
// way a configured rule is: without one they were an anonymous slice, and
// "turn off the net/http rule" had nowhere to point. The functions stay Go —
// they are pinned by named tests and some of them encode rules that took real
// effort to get right — they just stop being unaddressable.
type Builtin struct {
	ID   string
	Doc  string
	Fn   Recognizer
}

// Builtins is the rule set shipped with unfold. Which rules *should* be active
// is ultimately a per-project question (there's no point running Kafka rules
// against a repo that doesn't import it), but with a handful of stdlib-and-GCP
// rules the cost of running them all is a few string comparisons per call site.
var Builtins = []Builtin{
	{ID: "builtin.http.routes", Doc: "net/http route registration (inbound)", Fn: HTTPRoutes},
	{ID: "builtin.pubsub", Doc: "GCP Pub/Sub topics and subscriptions", Fn: PubSub},
	{ID: "builtin.http.calls", Doc: "http.Get/Post with a statically known URL (outbound)", Fn: HTTPClientCalls},
}

// Extract runs every enabled built-in over one call site. A disabled built-in
// contributes nothing, which is a user choice and reported as such elsewhere —
// an empty surface because a rule was switched off must not look like one
// nothing was found in.
func Extract(c Call, disabled map[string]bool) []model.Binding {
	var out []model.Binding
	for _, b := range Builtins {
		if disabled[b.ID] {
			continue
		}
		// Stamped here rather than in each recognizer: a rule that has to
		// remember to name itself is a rule that eventually forgets, and the
		// caller is the only place that knows which one is running.
		for _, bind := range b.Fn(c) {
			bind.Rule = b.ID
			out = append(out, bind)
		}
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

// IsMethodPath matches "/package.Service/Method" (and "/Service/Method" for
// protos declaring no package). Exported so the engine applies the same test
// when scanning a callee body.
//
// The shape alone isn't enough: "/api/health" also has two slashes and two
// identifiers. What separates a gRPC path is that the service half is
// fully-qualified or type-cased and the method half is an RPC name, which
// proto style makes UpperCamelCase. Without that, every HTTP route registered
// with a literal would be read as a gRPC call.
func IsMethodPath(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.Count(s, "/") != 2 {
		return false
	}
	svc, method, _ := strings.Cut(strings.TrimPrefix(s, "/"), "/")
	if !isIdentPath(svc) || !isIdent(method) || !startsUpper(method) {
		return false
	}
	return strings.Contains(svc, ".") || startsUpper(svc)
}

func startsUpper(s string) bool {
	return s != "" && s[0] >= 'A' && s[0] <= 'Z'
}

func isIdentPath(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !isIdent(part) {
			return false
		}
	}
	return true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
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
