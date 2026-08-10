// Package model holds the language-agnostic data types that flow between
// an indexing engine and the HTTP server / frontend. Both the Go engine
// (internal/indexer) and the TypeScript engine (internal/tsengine) produce
// these same JSON shapes, so the server and the React frontend never need
// to know which language a frame came from.
package model

import (
	"encoding/json"
	"errors"
)

// TargetID uniquely identifies a function/method within one loaded
// project. Its internal format is engine-specific and opaque to the
// server and frontend (Go uses *types.Func.FullName; the TS engine uses
// its own "<file>#<symbol>" scheme).
type TargetID string

// CallID uniquely identifies a single call site. Opaque to the frontend.
type CallID string

// CallKind classifies a call site by how its target is resolved. "Call site"
// is the older word for it: KindRef is a site where a function is named but
// not invoked, which is a place you can still expand from even though nothing
// runs there.
type CallKind string

const (
	KindDirect    CallKind = "direct"    // resolved to one specific function
	KindInterface CallKind = "interface" // dispatched through an interface; Candidates enumerates impls
	KindIndirect  CallKind = "indirect"  // through a function value, builtin, or otherwise unresolvable
	KindFanout    CallKind = "fanout"    // one site reaches many receivers (all run); Receivers enumerates them
	// KindRef is a value reference: the function named as a value rather than
	// called — passed as a callback, stored in a field, registered as a
	// handler. Expandable, because "what does that do" is the same question
	// you ask at a call; it just isn't answered by control flow arriving here,
	// so the UI must not let it read as a step in the trace.
	KindRef CallKind = "ref"
)

// Frame is the unit the frontend renders: a function's source plus the
// call sites inside it. Byte/character offsets in CallSite.SpanStart and
// SpanEnd are relative to Source (not to the original file). Each engine
// must emit offsets in the convention the frontend reads them with — the
// frontend indexes the JS (UTF-16) source string, so multibyte text must
// be accounted for consistently.
type Frame struct {
	ID TargetID `json:"id"`
	// Title is a short human-readable name for the frame (e.g. "Indexer.Frame"
	// or "English.greet"), used for the frame header and bookmark labels. The
	// ID is engine-specific and often not display-friendly (the TS engine's is
	// "<file>#<pos>"), so engines supply a clean Title.
	Title string `json:"title,omitempty"`
	File  string `json:"file"`
	// RelPath is where this body lives said the way the platform says it:
	// "<service>/<path within that service>". Set only in a workspace, where
	// the last couple of segments of an absolute path are the one thing that
	// can't tell you which service you're reading — and reading across
	// services is the entire point of splicing a remote frame in. Empty for a
	// single repo, and for a file that belongs to no repo in the workspace.
	RelPath   string     `json:"relPath,omitempty"`
	Language  string     `json:"language"` // "go", "typescript", "tsx", ...
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
	Source    string     `json:"source"`
	Calls     []CallSite `json:"calls"`
	// Diff, when non-nil, describes how this frame's source differs from the
	// same function in the configured diff base (see --diff-base). Attached by
	// the server only when a base engine is loaded. Nil = diff mode off.
	Diff *FrameDiff `json:"diff,omitempty"`
}

// FrameDiff annotates a Frame with how it differs from the diff base.
type FrameDiff struct {
	// Status is "added" (no matching function in the base), "modified" (source
	// differs), or "unchanged".
	Status string `json:"status"`
	// AddedLines holds the 0-based indices into Source of lines that are new or
	// changed relative to the base (the lines a reviewer should look at). Empty
	// for "added" (the whole frame is new) and "unchanged".
	AddedLines []int `json:"addedLines,omitempty"`
}

// CallSite describes one call inside a function body.
type CallSite struct {
	ID          CallID   `json:"id"`
	SpanStart   int      `json:"spanStart"`
	SpanEnd     int      `json:"spanEnd"`
	DisplayName string   `json:"displayName"`
	Kind        CallKind `json:"kind"`

	// TargetID is set for direct calls (the resolved target). Empty for
	// interface and indirect calls.
	TargetID TargetID `json:"targetId,omitempty"`

	// Candidates lists possible expansion targets for interface calls.
	// Empty for direct and indirect calls. The first candidate is the
	// default chosen when no choice is supplied.
	Candidates []Candidate `json:"candidates,omitempty"`

	// Goroutine is true when this call is launched asynchronously with the
	// `go` keyword (Go's `go f()`). It's orthogonal to Kind — a goroutine
	// launch is still a direct/interface/indirect call to its target — and
	// lets the frontend flag concurrency boundaries when reading a call path.
	Goroutine bool `json:"goroutine,omitempty"`

	// External is true when the resolved target lives outside the main
	// module (stdlib or a dependency). Still expandable by clicking, but
	// bulk "+1 level" expansion skips externals so a project trace isn't
	// buried under library bodies.
	External bool `json:"external,omitempty"`

	// Leaf, when set, is a rule's answer to "is expanding into this worth
	// doing" — replacing a single stdlib/dependency heuristic that couldn't
	// distinguish an in-house SDK you always want to expand from a logging
	// call you never do. It also makes "why can't I expand this?" answerable,
	// which the boolean never was.
	Leaf *LeafInfo `json:"leaf,omitempty"`

	// Receivers lists the targets a fan-out call reaches (all of them run,
	// unlike Candidates where one is chosen). Set only for kind="fanout".
	// FrameForCall(id, choice) selects Receivers[choice].
	Receivers  []Receiver `json:"receivers,omitempty"`
	FanoutKind string     `json:"fanoutKind,omitempty"` // e.g. "subscribers"
}

// LeafInfo marks a call site a rule classified as a boundary worth stopping
// at, and names what lies on the other side.
type LeafInfo struct {
	// Rule is the id that decided this, so "why is this a leaf" has an answer.
	Rule string `json:"rule"`
	// Label stands in for the callee's name — "→ orders" rather than the
	// generated method it happens to call.
	Label string `json:"label,omitempty"`
	// Key is the platform key the same rule emitted, which is what the far
	// end resolves against.
	Key  string `json:"key,omitempty"`
	Kind string `json:"kind,omitempty"`
	// Service, TargetTitle and TargetPath describe the far end when it is
	// already known — the service comes from the join, which costs nothing,
	// and the function comes from that service's index, so it is filled only
	// when that service happens to be indexed already. Resolving it here
	// otherwise would index another repository as a side effect of drawing a
	// frame, which is the cost the whole lazy workspace exists to avoid.
	//
	// TargetPath is "<service>/<path within it>:<line>", because a bare file
	// path is the one thing that can't say which service you'd be going to.
	Service     string `json:"service,omitempty"`
	TargetTitle string `json:"targetTitle,omitempty"`
	TargetPath  string `json:"targetPath,omitempty"`
	// CrossRepo offers the implementation in another repository: navigate to
	// it, or splice it in the way an ordinary call expands.
	CrossRepo bool `json:"crossRepo,omitempty"`
}

// Receiver is one target reached by a fan-out call (e.g. a subscriber of an
// observable). Unlike Candidate, fan-out receivers all run; Provenance and
// Confidence reflect that fan-out resolution is heuristic.
type Receiver struct {
	TargetID   TargetID `json:"targetId"`
	Label      string   `json:"label"`
	Provenance string   `json:"provenance,omitempty"` // e.g. "subscribe at app.ts:42"
	Confidence string   `json:"confidence,omitempty"` // "high" | "tentative"
}

// Candidate is one concrete implementation of an interface method, used to
// populate the impl-switcher dropdown.
type Candidate struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
}

// TypeInfo describes the symbol under a hovered source offset: its kind,
// name, type/signature, where it's defined, and (when it's a function the
// engine knows) a TargetID so the frontend can open it.
type TypeInfo struct {
	Kind      string   `json:"kind"`                // "var", "func", "type", "const", "field", "package", ...
	Name      string   `json:"name"`                // the identifier text
	Type      string   `json:"type"`                // type or signature, e.g. "func(s string) error"
	DefinedAt string   `json:"definedAt,omitempty"` // "<file>:<line>"
	Doc       string   `json:"doc,omitempty"`       // leading doc comment, if any
	TargetID  TargetID `json:"targetId,omitempty"`  // set when the symbol is a function/method we can open
	// Definition is the expanded shape of the symbol's type when it has
	// one worth showing — a named struct's fields, a named interface's
	// methods, or a named alias's underlying type. Multi-line, rendered
	// preformatted by the frontend. Empty when Type already says it all.
	Definition string `json:"definition,omitempty"`
	// Rules are the recognizers already matching the call site under the
	// pointer, built-in and configured alike. The hover card offers to author
	// a rule from this call; without knowing what already claims it, that
	// offer reads as "nothing recognizes this" even when three things do, and
	// the rule you write duplicates one you have.
	Rules []string `json:"rules,omitempty"`
	// Call describes the call site when the hovered symbol is the function
	// being called: the facts a rule can match on, as the evaluator will see
	// them.
	//
	// The authoring form used to derive the package from TargetID, which is
	// only set for functions this index holds — so a call through an
	// interface declared in a dependency yielded nothing, and the form fell
	// back to matching on the name alone. That is the case most in need of a
	// precise rule, since "Emit" or "Publish" names half the methods in the
	// ecosystem.
	Call *CallFacts `json:"call,omitempty"`
}

// CallFacts is what a rule can match on at one call site. It mirrors
// platform.Call, and is produced by the same extraction the evaluator uses —
// so a form built from it offers exactly the constraints that will hold.
type CallFacts struct {
	Package string     `json:"package,omitempty"`
	Recv    string     `json:"recv,omitempty"`
	RecvPkg string     `json:"recvPkg,omitempty"`
	Func    string     `json:"func,omitempty"`
	Args    []ArgFacts `json:"args,omitempty"`
}

// ArgFacts is one argument's matchable facts.
type ArgFacts struct {
	// Type is the static type of the value passed; ParamType the callee's
	// declared parameter type. They differ when a parameter is an interface
	// or `any`, which is exactly when one of them is the useful one.
	Type      string `json:"type,omitempty"`
	ParamType string `json:"paramType,omitempty"`
	// Value is the constant-folded string, when the argument has one. It's
	// what {argN} would expand to, so the form can show the key it's about to
	// produce rather than describing it.
	Value string `json:"value,omitempty"`
}

// SearchResult is one hit returned from an engine's Search.
type SearchResult struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	// External marks a hit that lives outside the repo that produced it —
	// stdlib or a dependency. Dependency code is loaded for resolution, so it
	// is searchable and worth keeping, but it is never what someone typing a
	// name is looking for first: it ranks below every service's own code.
	External bool `json:"external,omitempty"`
}

// UsageKind classifies how a target is referenced at a usage site.
type UsageKind string

const (
	// UsageCall is a direct call to the target.
	UsageCall UsageKind = "call"
	// UsageInterface is a call dispatched through an interface that the
	// target implements — execution *may* reach the target.
	UsageInterface UsageKind = "interface"
	// UsageRef is a non-call reference: the target used as a value
	// (passed as a callback, stored in a field, ...).
	UsageRef UsageKind = "ref"
)

// Usage is one place a target is referenced, with enough context for the
// frontend to render an excerpt strip and to splice the caller above the
// current view (re-rooting on Caller and expanding CallID with Choice
// reproduces this usage as an inline expansion).
type Usage struct {
	// CallID identifies the call site when the usage is an expandable call
	// the engine indexed. Empty for kind "ref".
	CallID CallID `json:"callId,omitempty"`
	// Choice is the candidate index that selects the target at that call
	// site (the FrameForCall choice). 0 for direct calls; for interface
	// calls it is the target's index in the call's Candidates.
	Choice int `json:"choice,omitempty"`

	Caller      TargetID  `json:"caller"`      // enclosing function
	CallerTitle string    `json:"callerTitle"` // display name of the caller
	File        string    `json:"file"`
	Line        int       `json:"line"` // 1-based file line of the usage
	Kind        UsageKind `json:"kind"`

	// Excerpt is a few source lines around the usage, clamped to the
	// caller's body. ExcerptLine is the 1-based file line of its first line
	// (so the usage line within the excerpt is Line - ExcerptLine).
	Excerpt     string `json:"excerpt"`
	ExcerptLine int    `json:"excerptLine"`
}

// BindingRole says which side of a cross-boundary edge a code site sits on.
type BindingRole string

const (
	// RoleInbound is a site where work enters this service: an HTTP route
	// registration, a subscription, a cron registration.
	RoleInbound BindingRole = "inbound"
	// RoleOutbound is a site where this service reaches out: a call to
	// another service, a publish to a topic, a query against a datastore.
	RoleOutbound BindingRole = "outbound"
)

// BindingConfidence records how a binding's key was resolved. The whole
// point of the tiering is that platform edges — unlike call edges resolved
// by go/types — are frequently not literal, so the UI must be able to say
// which kind of claim it is making.
type BindingConfidence string

const (
	// ConfExact is a literal ↔ literal match (or a folded constant).
	ConfExact BindingConfidence = "exact"
	// ConfDeclared comes from a manifest or infra-as-code declaration.
	ConfDeclared BindingConfidence = "declared"
	// ConfInferred is heuristic — the key is right but the role or the
	// endpoint is a guess.
	ConfInferred BindingConfidence = "inferred"
)

// BindingVisibility says how far a piece of inbound surface reaches. It's a
// more useful primary grouping than Kind: what you usually want to know about
// an entrypoint is who can get to it, not which library registered it.
type BindingVisibility string

const (
	// VisPublic is reachable from outside the platform — declared in the
	// manifest's publicRoutes.
	VisPublic BindingVisibility = "public"
	// VisPlatform is reachable by other services: a proto-declared RPC that
	// isn't excluded from SDK generation.
	VisPlatform BindingVisibility = "platform"
	// VisInternal is registered in code but named by neither list.
	VisInternal BindingVisibility = "internal"
)

// Binding is one place the code touches something outside itself, keyed by a
// string that the other side of the edge also names. Two bindings with the
// same Kind and Key are the two ends of one platform edge, which is how a
// publish gets joined to its subscriber (they share no AST edge, only a key).
//
// Within a single repo only one end is visible, so an outbound Binding whose
// key nothing here serves is expected, not an error.
type Binding struct {
	// ID identifies a binding within one ServiceView, so the crossing
	// relation below can name bindings without repeating them. It is an
	// index, not a durable handle: it changes when the surface does, which
	// is correct for a relation computed from that same surface.
	ID string `json:"id,omitempty"`

	Role BindingRole `json:"role"`
	// Kind namespaces the key: "http.route", "pubsub.topic",
	// "pubsub.subscription", "http.call".
	Kind string `json:"kind"`
	// Key is the join key — "POST /v1/orders", "orders-v1".
	Key string `json:"key"`
	// Detail is human-readable provenance ("mux.HandleFunc"), shown so a
	// surprising binding can be traced back to the call that produced it.
	Detail string `json:"detail,omitempty"`
	// Rule is the recognizer that produced this binding, built-in or
	// configured. Detail says what the call looked like; this says what
	// decided it *was* one — the difference between "surprising edge" and
	// "surprising edge, and here is the rule to switch off". Empty for
	// bindings no rule claims: the declared proto surface, which comes from a
	// manifest rather than from code.
	Rule string `json:"rule,omitempty"`

	// Target is the function the binding hands off to — an inbound route's
	// handler. Empty when the far end isn't in this index (every outbound
	// binding, and inbound registrations whose handler isn't a named func).
	Target      TargetID `json:"target,omitempty"`
	TargetTitle string   `json:"targetTitle,omitempty"`

	// Candidates lists the implementations when several match and none is
	// unambiguous — a service fronted by decorators, or one with generated
	// mocks alongside the real thing. Enumerating beats guessing, and beats
	// dropping the link entirely: the same choice interface call sites make.
	Candidates []Candidate `json:"candidates,omitempty"`

	// Site is the function containing the registration or call itself, so
	// every binding opens into source even when Target is empty.
	Site      TargetID `json:"site"`
	SiteTitle string   `json:"siteTitle,omitempty"`

	File string `json:"file"`
	Line int    `json:"line"`

	Confidence BindingConfidence `json:"confidence,omitempty"`

	// ServedBy names the workspace service that implements this outbound
	// key, and ServedByRepo its repo alias. Resolved from declarations
	// alone, so it's answerable without indexing that repo's Go code — the
	// implementation itself is fetched on demand via Resolve.
	ServedBy     string `json:"servedBy,omitempty"`
	ServedByRepo string `json:"servedByRepo,omitempty"`

	Visibility BindingVisibility `json:"visibility,omitempty"`

	// Stale marks a binding the manifest declares but the code doesn't
	// implement — a publicRoutes entry nothing registers, or a proto method
	// with no matching implementation. Declared facts are strong but they
	// rot, so a declaration the index can't corroborate is shown as suspect
	// rather than presented as real surface.
	Stale bool `json:"stale,omitempty"`

	// ReachesAnchor is set when a ServiceView was asked for an anchor and
	// this binding's handler transitively calls it — i.e. this is one of the
	// entrypoints through which the anchor actually runs.
	ReachesAnchor bool `json:"reachesAnchor,omitempty"`

	// ReachedByAnchor is the mirror image, for outbound: the anchored frame
	// transitively reaches this call site, so this is a call the anchor's
	// code path actually makes. "What runs me" and "what I run" are different
	// questions and want different walks — backwards for one, forwards for
	// the other.
	ReachedByAnchor bool `json:"reachedByAnchor,omitempty"`

	// Reaches lists the IDs of the outbound bindings this inbound entrypoint
	// can actually cause — the crossing relation, set on inbound bindings
	// only. It answers "if this route is hit, what does the service call?",
	// and read backwards, "what has to be hit for this call to happen?".
	//
	// One direction is stored and the other is derived, because the relation
	// is symmetric and storing both would double a payload that is already
	// the largest thing on the view.
	//
	// Absent is not the same as empty, and JSON can't tell them apart — an
	// omitted list means either "walked, found nothing" or "no handler to
	// walk from", which are different claims. CrossingKnown carries the
	// difference so the UI can say "reaches nothing" only when that was
	// actually determined.
	Reaches       []string `json:"reaches,omitempty"`
	CrossingKnown bool     `json:"crossingKnown,omitempty"`
}

// ServiceView is the zoomed-out picture of one service: what enters it and
// what it reaches out to. It is not a separate index — it's the same binding
// data grouped by role, which is why zooming out costs nothing beyond the
// recognizer pass that produced the bindings.
type ServiceView struct {
	Name   string `json:"name"`             // repo/module directory name
	Module string `json:"module,omitempty"` // Go module path, informational
	Root   string `json:"root,omitempty"`   // project directory
	// ProtoRoot is the shared proto repository currently configured, so the
	// UI can show what's set and offer to change it.
	ProtoRoot string `json:"protoRoot,omitempty"`
	// NeedsProtoRoot is true when the manifest declares protoPaths that
	// can't be resolved yet — the cue for the UI to offer a picker rather
	// than just reporting a warning.
	NeedsProtoRoot bool `json:"needsProtoRoot,omitempty"`

	// Anchor is the frame the user zoomed out from, carried up so every
	// level can mark what reaches it. Empty when zooming out from nothing.
	Anchor      TargetID `json:"anchor,omitempty"`
	AnchorTitle string   `json:"anchorTitle,omitempty"`

	Inbound  []Binding `json:"inbound"`
	Outbound []Binding `json:"outbound"`

	// Repos lists the workspace's repositories when more than one is open,
	// including which are indexed — a lazy workspace can only search and
	// cross-link what it has loaded, and saying so beats looking broken.
	Repos []RepoInfo `json:"repos,omitempty"`

	// OutboundUnreachable counts outbound calls excluded because execution
	// can't reach them from any entrypoint this engine recognized. Surfaced
	// so an empty column can be told apart from a router unfold can't read.
	OutboundUnreachable int `json:"outboundUnreachable,omitempty"`

	// Warning explains why part of the view may be missing — most often a
	// declared proto surface that couldn't be loaded. An empty surface and a
	// misconfigured proto root look identical without it.
	Warning string `json:"warning,omitempty"`
}

// RepoInfo describes one repository in a workspace.
type RepoInfo struct {
	Alias   string `json:"alias"` // stable key used to namespace ids
	Name    string `json:"name"`  // display name (the manifest's, usually)
	Dir     string `json:"dir"`
	Primary bool   `json:"primary,omitempty"` // the repo unfold was pointed at
	Indexed bool   `json:"indexed,omitempty"` // its Go code is loaded
	Error   string `json:"error,omitempty"`
}

// Resolution is the answer to "open the implementation of this key". Target
// is empty when the serving repo is known but its implementation couldn't be
// identified, in which case Note says so.
type Resolution struct {
	Repo    string   `json:"repo"`
	Service string   `json:"service"`
	Target  TargetID `json:"target,omitempty"`
	Title   string   `json:"title,omitempty"`
	Stale   bool     `json:"stale,omitempty"`
	Note    string   `json:"note,omitempty"`
	// Candidates is set instead of Target when the serving repo has several
	// implementations of the key and none is unambiguous.
	Candidates []Candidate `json:"candidates,omitempty"`
}

// PlatformView is the L0 picture: every service in the workspace and the
// calls between them.
//
// Services come from the declaration layer, so they're all listed however
// little has been indexed. Edges cannot: knowing that A calls B means having
// read A's code. So a service that hasn't been indexed contributes no
// outbound edges, and says so rather than looking like a leaf.
type PlatformView struct {
	Services []PlatformService `json:"services"`
	Edges    []PlatformEdge    `json:"edges"`

	// Anchor is the frame this view was zoomed out from, carried up so the
	// platform level can mark what reaches it — the same anchor the service
	// level uses, one granularity further out.
	Anchor      TargetID `json:"anchor,omitempty"`
	AnchorTitle string   `json:"anchorTitle,omitempty"`
}

// PlatformService is one node.
type PlatformService struct {
	Alias   string `json:"alias"`
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Primary bool   `json:"primary,omitempty"`
	// Indexed reports whether this service's code has been read. Its
	// *incoming* edges are known either way — those come from other
	// services' code — but its outgoing ones are unknown until it is.
	Indexed bool   `json:"indexed,omitempty"`
	Error   string `json:"error,omitempty"`
	// Methods counts the RPCs it declares, which is known from protos alone.
	Methods int `json:"methods,omitempty"`
	// ReachesAnchor is set on the service the anchor lives in, and on any
	// service calling an API of it that leads to the anchor.
	ReachesAnchor bool `json:"reachesAnchor,omitempty"`
}

// PlatformEdge is every call from one service to another, of one kind.
type PlatformEdge struct {
	From  string         `json:"from"` // service alias
	To    string         `json:"to"`
	Kind  string         `json:"kind"`
	Calls []PlatformCall `json:"calls"`
	// ReachesAnchor is set when any call along this edge lands on an API
	// whose implementation reaches the anchor.
	ReachesAnchor bool `json:"reachesAnchor,omitempty"`
}

// PlatformCall is one call site behind an edge, so a platform-level line can
// be opened as code.
type PlatformCall struct {
	Key string `json:"key"`
	// ReachesAnchor marks this specific RPC as one that leads to the anchor.
	ReachesAnchor bool     `json:"reachesAnchor,omitempty"`
	Site          TargetID `json:"site,omitempty"`
	SiteTitle     string   `json:"siteTitle,omitempty"`
	File          string   `json:"file,omitempty"`
	Line          int      `json:"line,omitempty"`
}

// WorkspaceEngine is implemented by engines that can describe a whole
// workspace. Separate from PlatformEngine because a single repo has a service
// view but no platform above it.
type WorkspaceEngine interface {
	PlatformView(anchor TargetID) (*PlatformView, error)
	// ServiceViewOf describes any service in the workspace, not just the one
	// unfold was pointed at — otherwise picking a service at the platform
	// level would zoom in on somebody else.
	ServiceViewOf(repo string, anchor TargetID) (*ServiceView, error)
}

// HasWorkspace reports whether a workspace of several repositories is open,
// so the UI offers the platform level only when there's a platform to show.
// A wrapper answers for whatever engine it holds, since its own method set
// can't be conditional.
func HasWorkspace(e Engine) bool {
	if p, ok := e.(interface{ WorkspaceAvailable() bool }); ok {
		return p.WorkspaceAvailable()
	}
	_, ok := e.(WorkspaceEngine)
	return ok
}

// CrossRepoResolver is implemented by engines that federate repositories.
type CrossRepoResolver interface {
	Resolve(kind, key string) (*Resolution, error)
}

// ServiceSearcher is implemented by engines that can bias search toward one
// service. Which service is "current" is a property of what's on screen, not
// of the engine — you can zoom into any service in the workspace — so it
// arrives per request rather than being fixed at load. An empty repo means
// the primary one, which is what plain Search assumes.
type ServiceSearcher interface {
	SearchFrom(repo, query string, limit int) []SearchResult
}

// PlatformEngine is the optional half of Engine: engines that can describe
// their project as a service implement it, and the server exposes
// /api/service only when the loaded engine does. Kept separate from Engine
// so an engine without recognizers (today, the TypeScript one) stays valid.
type PlatformEngine interface {
	// ServiceView returns the service-level view. A non-empty anchor marks
	// the bindings that reach it; an anchor that isn't an indexed function
	// is ignored rather than being an error.
	ServiceView(anchor TargetID) (*ServiceView, error)
}

// ErrNoPlatformView is what a wrapping engine returns when the engine it
// currently holds has no recognizers. A wrapper can't implement
// PlatformEngine conditionally — the method set is static — so it satisfies
// the interface and reports the gap at call time instead.
var ErrNoPlatformView = errors.New("platform view is not available for this engine")

// ErrNoWorkspace is returned when a cross-repo hop is requested but only one
// repository is open.
var ErrNoWorkspace = errors.New("no workspace is open (start unfold with --workspace)")

// HasPlatformView reports whether e can currently serve a service-level view,
// so the server can advertise the zoom-out affordance only when it works.
// A wrapper answers for whatever engine it holds; anything else is judged by
// whether it implements PlatformEngine at all.
func HasPlatformView(e Engine) bool {
	if p, ok := e.(interface{ PlatformAvailable() bool }); ok {
		return p.PlatformAvailable()
	}
	_, ok := e.(PlatformEngine)
	return ok
}

// Engine is the query surface the HTTP server depends on. It is the seam
// that lets unfold support multiple languages: the server is constructed
// with an Engine and never references a concrete indexer. Construction and
// project loading are engine-specific and happen before the server starts.
type Engine interface {
	// LookupSymbol resolves a symbol name (qualified or bare) to a target.
	LookupSymbol(name string) (TargetID, error)
	// Frame returns the frame for a target.
	Frame(id TargetID) (*Frame, error)
	// FrameForCall returns the frame for the chosen target of a call site.
	// choice selects among interface candidates (ignored for direct calls).
	FrameForCall(id CallID, choice int) (*Frame, error)
	// Search returns up to limit symbols matching query.
	Search(query string, limit int) []SearchResult
	// Files lists the absolute paths of the indexed source files, so the
	// frontend can show a file tree. A whole-file Frame is obtained by
	// passing "file:<path>" as a target id to Frame.
	Files() []string
	// TypeInfo resolves the symbol at a UTF-16 offset into the frame's
	// Source and returns its type details. Returns nil (no error) when the
	// offset isn't over a resolvable symbol.
	TypeInfo(id TargetID, offset int) (*TypeInfo, error)
	// Usages returns the places the target is referenced inside indexed
	// function bodies: direct calls, interface-dispatched calls that may
	// reach it, and value references. Sorted by file then line.
	Usages(id TargetID) ([]Usage, error)
}

// RuleInfo describes one recognizer for display: what it is, whether it's on,
// where it came from, and how much it actually matched.
type RuleInfo struct {
	ID      string `json:"id"`
	Doc     string `json:"doc,omitempty"`
	Builtin bool   `json:"builtin,omitempty"`
	Enabled bool   `json:"enabled"`
	// Source is the file a configured rule came from, so "why is this edge
	// here" is answerable down to the file someone else committed.
	Source string `json:"source,omitempty"`
	// Matches is how many bindings it produced. Zero on a rule that is
	// supposed to be doing something is the signal that a library moved.
	Matches int `json:"matches"`
	// Spec is the rule as written, so it can be read and edited where it is
	// seen. A list of ids and counts can tell you a rule stopped matching; it
	// can't tell you what it was looking for, which is the next thing anyone
	// asks. Empty for built-ins — their body is Go, and the only thing about
	// them that is editable is Enabled.
	Spec json.RawMessage `json:"spec,omitempty"`
}

// RuleReport is the whole recognizer picture, including what went wrong
// assembling it — a dropped rule that nobody is told about is exactly the
// failure the rule system exists to avoid.
type RuleReport struct {
	Rules    []RuleInfo `json:"rules"`
	Problems []string   `json:"problems,omitempty"`
}
