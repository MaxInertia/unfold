// Package rules turns configuration into recognizers, so supporting a new
// in-house wrapper or message library is a file someone edits rather than Go
// someone writes.
//
// A rule says only *what shape counts*. It deliberately cannot express
// reachability filtering, attribution to the owning caller, or deduplication:
// those are properties of the evaluator, applied uniformly to whatever any
// rule produces. They were the hardest part of the built-in gRPC pass to get
// right and they are pinned by named tests; letting every rule author
// re-specify them is how they get re-specified badly.
//
// The vocabulary is deliberately small, and every primitive in it exists to
// express a *shape* rather than a particular library. The test of that is the
// built-ins: if a rule set can't state net/http routing, GCP Pub/Sub and the
// gRPC client pattern, the vocabulary is wrong.
package rules

import (
	"encoding/json"
	"fmt"
	"strings"
)

// File is the on-disk shape. JSON rather than YAML because the authoring UI is
// the main producer; a "//" key carries a comment where one is genuinely
// needed, since JSON has nowhere else to put one.
type File struct {
	Comment string `json:"//,omitempty"`
	Rules   []Rule `json:"rules"`
	// Serves is what a repository states it serves without any code in it
	// saying so. See Serve.
	Serves []Serve `json:"serves,omitempty"`
}

// Serve is an API a service states it serves.
//
// It is the general form of what `microservice.yaml` + `--proto-root` does for
// one organisation's gRPC: a *declaration* that a service is the inbound end
// of a set of keys. Declarations matter even now that a registration is read
// from code, and for two reasons that code can't cover:
//
//   - They cost no index. The whole point of the declared tier is that a
//     workspace can answer "who serves this key" for repositories nobody has
//     opened yet — which is the difference between a cross-repo hop that works
//     immediately and one that works after a minute of indexing.
//   - They cover what this index can't read: a service in a language unfold
//     doesn't index, an API served by a gateway rather than by code, a
//     transport nobody has written a recognizer for.
//
// A declared key that the code *does* corroborate is not stated twice: the
// binding read from code wins, since it knows where the implementation is.
type Serve struct {
	Comment string `json:"//,omitempty"`
	// Service names the repository this entry is about, by directory name or
	// by the name its manifest gives it. Empty means "the repository this file
	// belongs to", which is the only thing a repo-local file can sensibly mean
	// — and the reason an entry in a shared file must name one.
	Service string `json:"service,omitempty"`
	// Kind namespaces the key, exactly as a rule's emit does: "grpc.method",
	// "http.route", "pubsub.subscription", or a kind of your own.
	Kind string `json:"kind"`
	// Keys are the keys served. A key ending in "/*" stands for every key
	// under that prefix — "notes.v1.NotesService/*" is the whole service —
	// which is what makes declaring a gRPC surface a line rather than a list.
	//
	// A pattern answers "does this service serve this key" and cannot
	// enumerate: nothing here knows the method names, so a wildcard is shown
	// as itself rather than expanded into a surface it would be inventing.
	Keys []string `json:"keys"`
	// From is the file this entry was read from, filled in by Load. It is what
	// makes "why does unfold think billing serves this" answerable.
	From string `json:"-"`
}

// Wildcard is the suffix that makes a key a prefix pattern.
const Wildcard = "/*"

// Matches reports whether a declared key covers the key being looked up.
func KeyMatches(declared, key string) bool {
	if declared == key {
		return true
	}
	prefix, ok := strings.CutSuffix(declared, Wildcard)
	return ok && strings.HasPrefix(key, prefix+"/")
}

// Rule is one recognizer: a predicate over call sites, and what to emit when
// it holds.
//
// A rule with no Emit is a *settings* entry — the way a built-in is switched
// off, since built-ins are addressed by id and have no configurable body.
type Rule struct {
	Comment string `json:"//,omitempty"`
	ID      string `json:"id"`
	// Enabled defaults to true. It is a pointer so "absent" and "false" are
	// distinguishable: a file that lists a rule id purely to disable it must
	// not be read as re-enabling it.
	Enabled *bool  `json:"enabled,omitempty"`
	Match   *Match `json:"match,omitempty"`
	Emit    *Emit  `json:"emit,omitempty"`
	// Classifier marks a rule that exists only to be named by another rule's
	// calleeMatches. It still computes a key — that's what {inner.key} reads —
	// but produces no binding of its own.
	//
	// This is not a convenience. A generated client's body issuing a transport
	// call is what *identifies* the client; it is not itself an edge the
	// service makes. Without the distinction, every generated stub in the repo
	// becomes an outbound edge, which is the exact failure the built-in pass
	// avoids by treating a stub as capability rather than as a call.
	Classifier bool `json:"classifier,omitempty"`
	// Leaf classifies a matching call site rather than emitting an edge:
	// whether expanding into it is worth doing.
	//
	// The decision it replaces is one hardcoded boolean — a call is a leaf iff
	// its target is stdlib or a dependency — which cannot give three answers
	// it needs to. An in-house SDK *is* a dependency and is exactly what you
	// want to expand; a logging library is expandable and never worth
	// expanding; a client fronting another service shouldn't expand into
	// transport plumbing at all, it should offer the handler in the other repo.
	Leaf *Leaf `json:"leaf,omitempty"`
}

// Leaf says how a matching call site should be treated when reading code.
type Leaf struct {
	// Expand overrides the built-in stdlib/dependency heuristic in either
	// direction: false makes a call a leaf that isn't one by default, true
	// rescues an in-house SDK from being skipped by bulk expansion.
	Expand *bool `json:"expand,omitempty"`
	// Label is shown on the leaf instead of the callee's name — "→ orders"
	// reads better than the generated method it actually calls. Supports the
	// same template vocabulary as a key, so a capture can name the service.
	Label string `json:"label,omitempty"`
	// CrossRepo offers the far end: navigate to, or inline, the
	// implementation the emitted key resolves to in another repository.
	CrossRepo bool `json:"crossRepo,omitempty"`
}

// Match is the predicate. Every field is optional and all present fields must
// hold — an empty Match matches everything, which validation rejects, because
// a rule that fires on every call site is never what anyone meant.
type Match struct {
	// Package is the callee's package path. Supports "*" wildcards and
	// "$(name)" captures, so one rule covers a whole family of SDKs:
	// "foo/$(domain)/sdks/go".
	Package string `json:"package,omitempty"`
	// Recv is the receiver's bare type name ("Client"); RecvPkg its package.
	// Empty Recv matches a package-level function *and* a method, unless
	// RecvOnly is set — the distinction matters for net/http, where
	// HandleFunc exists both ways.
	Recv     string `json:"recv,omitempty"`
	RecvPkg  string `json:"recvPkg,omitempty"`
	RecvOnly bool   `json:"recvOnly,omitempty"`
	// Func is the callee's name, wildcards allowed.
	Func string `json:"func,omitempty"`
	// MinArgs rejects an overload with too few arguments to carry the key.
	MinArgs int `json:"minArgs,omitempty"`
	// Args constrains individual arguments by type. This is what makes a rule
	// about a *library* rather than about a verb: "Emit" names half the
	// event-publishing methods ever written, while an argument of type
	// github.com/acme/events/pb.Event names exactly one library's.
	//
	// It matters most where the other discriminators can't reach. A call
	// through an interface carries the interface's package as the receiver —
	// and nothing stops a repo from declaring its own interface with the same
	// method, so RecvPkg identifies the declaration site rather than the
	// library. The types crossing the call don't move.
	Args []ArgMatch `json:"args,omitempty"`

	// CalleeMatches is the second phase: this call site counts only if the
	// function being called itself contains a call matching another rule.
	// That is what recognizes a wrapper — an SDK method is interesting
	// because of what it does inside, not because of its name.
	CalleeMatches *Nested `json:"calleeMatches,omitempty"`
}

// ArgMatch constrains one argument position.
//
// Two type fields rather than one, because a call can make them differ and
// which one discriminates depends on the library:
//
//   - Type is what the caller passed. Use it when the parameter is `any` or an
//     interface, where the declaration says nothing.
//   - ParamType is what the callee's signature declares. Use it when the value
//     passed is a local implementation of the library's interface, or nil,
//     where the call site says nothing.
//
// Both accept "*" wildcards and are written fully qualified
// ("*github.com/acme/events/pb.Event"), for the same reason package paths are:
// short type names collide across modules. Giving both means both must hold.
type ArgMatch struct {
	// Index is the argument position, zero-based. Required — an unanchored
	// "some argument is an Event" would match a call that merely mentions the
	// type somewhere, which is not what anyone means by it.
	Index int `json:"index"`
	// Type is the static type of the value passed.
	Type string `json:"type,omitempty"`
	// ParamType is the callee's declared parameter type at this position; for
	// a variadic parameter, its element type.
	ParamType string `json:"paramType,omitempty"`
}

// Nested is a reference to another rule, evaluated against a callee's body.
type Nested struct {
	Rule string `json:"rule"`
	// Depth is how far into the callee's own callees to look. 1 means the
	// callee's own body only, which is the safe default and the one the
	// built-in gRPC pass settled on after unbounded following produced edges
	// for services that were never called. Raising it is a deliberate act.
	Depth int `json:"depth,omitempty"`
}

// Emit is what a matching call site produces.
type Emit struct {
	Role string `json:"role"` // "inbound" | "outbound"
	Kind string `json:"kind"` // "pubsub.topic", "http.route", ...
	// Key is a template over the match: "{arg0}", "{inner.key}",
	// "{domain}" for a capture, or any combination.
	Key string `json:"key"`
	// Handler names the argument holding the function value that serves this
	// binding ("arg1"), which is how a subscriber registration finds its
	// subscriber. Empty when the binding has no in-repo handler.
	Handler string `json:"handler,omitempty"`
	// Confidence is "declared" by default and may be lowered to "inferred".
	// It can never be "exact": that means literal-to-literal or a shared
	// generated stub, and a human asserting that a call shape means "publish"
	// is a different claim that the badge has to keep distinct.
	Confidence string `json:"confidence,omitempty"`
}

// On reports whether the rule is enabled, defaulting to true.
func (r Rule) On() bool { return r.Enabled == nil || *r.Enabled }

// Parse reads a rules file, returning the rules and any problems with them.
// A malformed file is an error; individually invalid rules are reported and
// skipped, so one bad entry doesn't cost the whole file.
func Parse(data []byte) (*File, []error, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("parse rules: %w", err)
	}
	var problems []error
	kept := f.Rules[:0]
	seen := map[string]bool{}
	for _, r := range f.Rules {
		if err := r.validate(seen); err != nil {
			problems = append(problems, err)
			continue
		}
		seen[r.ID] = true
		kept = append(kept, r)
	}
	f.Rules = kept

	keptServes := f.Serves[:0]
	for _, sv := range f.Serves {
		if err := sv.validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		keptServes = append(keptServes, sv)
	}
	f.Serves = keptServes
	return &f, problems, nil
}

func (s Serve) validate() error {
	if strings.TrimSpace(s.Kind) == "" {
		return fmt.Errorf("serves entry: kind is required — it namespaces the key")
	}
	if len(s.Keys) == 0 {
		return fmt.Errorf("serves entry for kind %q: no keys, so it declares nothing", s.Kind)
	}
	for _, k := range s.Keys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("serves entry for kind %q: an empty key joins to nothing", s.Kind)
		}
		// A bare "*" would make one service the answer for every key of its
		// kind, which is never what anyone means and looks like a tool bug
		// rather than a config mistake — the same reason an empty match is
		// refused at the door.
		if k == Wildcard || k == "*" {
			return fmt.Errorf("serves entry for kind %q: %q claims every key of its kind", s.Kind, k)
		}
	}
	return nil
}

func (r Rule) validate(seen map[string]bool) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("rule with no id")
	}
	if seen[r.ID] {
		return fmt.Errorf("rule %q: duplicate id", r.ID)
	}
	// A settings-only entry (id + enabled) is how a built-in is switched off.
	if r.Match == nil && r.Emit == nil && r.Leaf == nil {
		return nil
	}
	if r.Match != nil {
		for _, a := range r.Match.Args {
			if a.Index < 0 {
				return fmt.Errorf("rule %q: argument index %d is negative", r.ID, a.Index)
			}
			// An index alone constrains nothing, and a rule whose author
			// believed otherwise is one that matches far more than intended —
			// exactly the mistake that is cheapest to catch here.
			if a.Type == "" && a.ParamType == "" {
				return fmt.Errorf("rule %q: argument %d has no type or paramType, so it narrows nothing", r.ID, a.Index)
			}
		}
	}
	// A leaf-only rule classifies call sites without producing an edge.
	if r.Match != nil && r.Emit == nil && r.Leaf != nil {
		if r.Match.isEmpty() {
			return fmt.Errorf("rule %q: match is empty, which would match every call site", r.ID)
		}
		return nil
	}
	if r.Match == nil || r.Emit == nil {
		return fmt.Errorf("rule %q: needs both match and emit (or neither, to toggle a built-in)", r.ID)
	}
	if r.Match.isEmpty() {
		// A rule that fires on every call site is never intended, and the
		// damage — a surface full of noise — looks like a tool bug rather
		// than a config mistake, so it's refused at the door.
		return fmt.Errorf("rule %q: match is empty, which would match every call site", r.ID)
	}
	if r.Emit.Role != "inbound" && r.Emit.Role != "outbound" {
		return fmt.Errorf("rule %q: role must be inbound or outbound, got %q", r.ID, r.Emit.Role)
	}
	if strings.TrimSpace(r.Emit.Kind) == "" {
		return fmt.Errorf("rule %q: kind is required — it namespaces the join key", r.ID)
	}
	if strings.TrimSpace(r.Emit.Key) == "" {
		return fmt.Errorf("rule %q: key is required — a binding with no key joins to nothing", r.ID)
	}
	if r.Emit.Confidence == "exact" {
		return fmt.Errorf("rule %q: confidence cannot be exact; a configured shape is declared or inferred", r.ID)
	}
	if c := r.Emit.Confidence; c != "" && c != "declared" && c != "inferred" {
		return fmt.Errorf("rule %q: confidence must be declared or inferred, got %q", r.ID, c)
	}
	if n := r.Match.CalleeMatches; n != nil {
		if strings.TrimSpace(n.Rule) == "" {
			return fmt.Errorf("rule %q: calleeMatches needs a rule id", r.ID)
		}
		if n.Rule == r.ID {
			return fmt.Errorf("rule %q: calleeMatches refers to itself", r.ID)
		}
		if n.Depth < 0 {
			return fmt.Errorf("rule %q: calleeMatches depth cannot be negative", r.ID)
		}
	}
	return nil
}

func (m *Match) isEmpty() bool {
	return m.Package == "" && m.Recv == "" && m.RecvPkg == "" && m.Func == "" &&
		m.MinArgs == 0 && m.CalleeMatches == nil && len(m.constrainedArgs()) == 0
}

// constrainedArgs are the argument matchers that actually constrain something.
// An entry with an index and no types narrows nothing, so it must not be what
// rescues a rule from being empty — that would turn "match everything" into a
// rule the door lets through.
func (m *Match) constrainedArgs() []ArgMatch {
	var out []ArgMatch
	for _, a := range m.Args {
		if a.Type != "" || a.ParamType != "" {
			out = append(out, a)
		}
	}
	return out
}

// errf is fmt.Errorf without importing fmt at every call site in this package.
func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
