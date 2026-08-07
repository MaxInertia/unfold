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

	// CalleeMatches is the second phase: this call site counts only if the
	// function being called itself contains a call matching another rule.
	// That is what recognizes a wrapper — an SDK method is interesting
	// because of what it does inside, not because of its name.
	CalleeMatches *Nested `json:"calleeMatches,omitempty"`
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
	return &f, problems, nil
}

func (r Rule) validate(seen map[string]bool) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("rule with no id")
	}
	if seen[r.ID] {
		return fmt.Errorf("rule %q: duplicate id", r.ID)
	}
	// A settings-only entry (id + enabled) is how a built-in is switched off.
	if r.Match == nil && r.Emit == nil {
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
		m.MinArgs == 0 && m.CalleeMatches == nil
}

// errf is fmt.Errorf without importing fmt at every call site in this package.
func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
