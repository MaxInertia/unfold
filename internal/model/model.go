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
)

// Frame is the unit the frontend renders: a function's source plus the
// call sites inside it. Byte/character offsets in CallSite.SpanStart and
// SpanEnd are relative to Source (not to the original file). Each engine
// must emit offsets in the convention the frontend reads them with — the
// frontend indexes the JS (UTF-16) source string, so multibyte text must
// be accounted for consistently.
type Frame struct {
	ID        TargetID   `json:"id"`
	File      string     `json:"file"`
	Language  string     `json:"language"` // "go", "typescript", "tsx", ...
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
	Source    string     `json:"source"`
	Calls     []CallSite `json:"calls"`
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
}

// Candidate is one concrete implementation of an interface method, used to
// populate the impl-switcher dropdown.
type Candidate struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
}

// SearchResult is one hit returned from an engine's Search.
type SearchResult struct {
	TargetID TargetID `json:"targetId"`
	Label    string   `json:"label"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
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
}
