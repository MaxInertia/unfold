// Package model holds the language-agnostic data types that flow between
// an indexing engine and the HTTP server / frontend. Both the Go engine
// (internal/indexer) and the TypeScript engine (internal/tsengine) produce
// these same JSON shapes, so the server and the React frontend never need
// to know which language a frame came from.
package model

// TargetID uniquely identifies a function/method within one loaded
// project. Its internal format is engine-specific and opaque to the
// server and frontend (Go uses *types.Func.FullName; the TS engine uses
// its own "<file>#<symbol>" scheme).
type TargetID string

// CallID uniquely identifies a single call site. Opaque to the frontend.
type CallID string

// CallKind classifies a call site by how its target is resolved.
type CallKind string

const (
	KindDirect    CallKind = "direct"    // resolved to one specific function
	KindInterface CallKind = "interface" // dispatched through an interface; Candidates enumerates impls
	KindIndirect  CallKind = "indirect"  // through a function value, builtin, or otherwise unresolvable
	KindFanout    CallKind = "fanout"    // one site reaches many receivers (all run); Receivers enumerates them
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
	Title     string     `json:"title,omitempty"`
	File      string     `json:"file"`
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

	// Receivers lists the targets a fan-out call reaches (all of them run,
	// unlike Candidates where one is chosen). Set only for kind="fanout".
	// FrameForCall(id, choice) selects Receivers[choice].
	Receivers  []Receiver `json:"receivers,omitempty"`
	FanoutKind string     `json:"fanoutKind,omitempty"` // e.g. "subscribers"
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
}

// SearchResult is one hit returned from an engine's Search.
type SearchResult struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
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
