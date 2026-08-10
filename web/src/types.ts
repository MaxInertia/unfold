// Mirrors Frame and CallSite in internal/indexer/indexer.go.

export type TargetID = string;
export type CallID = string;
// "ref" is a site where the function is named but not called — a callback
// passed, a handler registered. Expandable like the rest; it just isn't a step
// in the trace, so it never joins bulk expansion and says so where it renders.
export type CallKind = "direct" | "interface" | "indirect" | "fanout" | "ref";

export interface CallSite {
  id: CallID;
  spanStart: number; // byte offsets in Frame.source
  spanEnd: number;
  displayName: string;
  kind: CallKind;
  targetId?: TargetID; // present for direct calls
  candidates?: Candidate[]; // present for interface calls with known impls
  goroutine?: boolean; // call is launched with the `go` keyword
  external?: boolean; // target is stdlib/dependency; bulk expansion skips it
  // A rule's answer to "is expanding into this worth doing", replacing a
  // single stdlib/dependency heuristic that couldn't tell an in-house SDK you
  // always want to expand from a logging call you never do.
  leaf?: LeafInfo;
  receivers?: Receiver[]; // present for fan-out calls (all of them run)
  fanoutKind?: string; // e.g. "subscribers"
}

export interface LeafInfo {
  rule: string; // which rule decided, so "why is this a leaf" has an answer
  label?: string;
  key?: string;
  kind?: string;
  crossRepo?: boolean;
  // Where the boundary leads, when that's already known. `service` comes from
  // the join and is always there for a resolvable key; the function is only
  // filled when the far service is already indexed (see LeafInfo in the Go
  // model — resolving it eagerly would index another repo to draw a frame).
  service?: string;
  targetTitle?: string;
  targetPath?: string; // "<service>/<path within it>:<line>"
}

export interface Candidate {
  targetId: TargetID;
  label: string;
}

export interface Receiver {
  targetId: TargetID;
  label: string;
  provenance?: string;
  confidence?: string; // "high" | "tentative"
}

export interface Frame {
  id: TargetID;
  title?: string; // display-friendly name; falls back to a prettified id
  file: string;
  // "<service>/<path within it>" — set only in a workspace, where the tail of
  // an absolute path can't say which service the body belongs to.
  relPath?: string;
  language: string; // "go"
  startLine: number;
  endLine: number;
  source: string;
  calls: CallSite[];
  diff?: FrameDiff; // present only in --diff-base mode
}

// How a frame differs from the diff base (see --diff-base).
export interface FrameDiff {
  status: "added" | "modified" | "unchanged";
  addedLines?: number[]; // 0-based indices into source of new/changed lines
}

export interface SearchResult {
  targetId: TargetID;
  label: string;
  file: string;
  line: number;
  // A hit in stdlib or a dependency rather than a service's own code. Already
  // ranked last by the server; the flag lets the picker say so.
  external?: boolean;
}

// One place a target is referenced (mirrors model.Usage). callId + choice
// reproduce the usage as an inline expansion via FrameForCall, which is how
// "splice the caller above" re-roots the view.
export interface Usage {
  callId?: CallID; // empty for kind "ref"
  choice?: number; // candidate index selecting the target at that call
  caller: TargetID; // enclosing function
  callerTitle: string;
  file: string;
  line: number; // 1-based file line of the usage
  kind: "call" | "interface" | "ref";
  excerpt: string; // context lines, clamped to the caller's body
  excerptLine: number; // 1-based file line of excerpt's first line
}

// ----- platform / zoom-out (mirrors model.Binding and model.ServiceView) -----

export type BindingRole = "inbound" | "outbound";
export type BindingConfidence = "exact" | "declared" | "inferred";
// How far a piece of inbound surface reaches — a more useful primary grouping
// than kind, since what you want to know about an entrypoint is who can get
// to it.
export type BindingVisibility = "public" | "platform" | "internal";

// One place the code touches something outside itself, keyed by a string the
// other end of the edge also names. Within a single repo only one end is
// visible, so an outbound binding with no matching inbound one is normal.
export interface Binding {
  // Identifies this binding within one ServiceView, so the crossing relation
  // can name bindings without repeating them. Not durable across reindexes.
  id?: string;
  role: BindingRole;
  kind: string; // "http.route" | "pubsub.topic" | "pubsub.subscription" | "http.call"
  key: string; // the join key, e.g. "POST /v1/orders"
  detail?: string; // provenance, e.g. "ServeMux.HandleFunc"
  target?: TargetID; // the handler, when it's an indexed function
  targetTitle?: string;
  // Set instead of target when several implementations match and none is
  // unambiguous — a service behind decorators, say. Enumerating beats both
  // guessing and dropping the link.
  candidates?: Candidate[];
  site: TargetID; // the function containing the registration/call
  siteTitle?: string;
  file: string;
  line: number;
  confidence?: BindingConfidence;
  visibility?: BindingVisibility;
  // The workspace service implementing this outbound key, resolved from
  // declarations alone — naming it costs no Go index. Opening it does, which
  // is why that goes through /api/resolve.
  servedBy?: string;
  servedByRepo?: string;
  // Declared by the manifest but not implemented in code — a publicRoutes
  // entry nothing registers, or a proto method with no implementation.
  stale?: boolean;
  reachesAnchor?: boolean; // this entrypoint transitively reaches the anchor
  // The mirror, for outbound: the anchored frame reaches this call site, so
  // it's a call the anchor's code path actually makes.
  reachedByAnchor?: boolean;
  // The crossing relation, on inbound bindings only: the outbound bindings
  // this entrypoint can cause. The other direction is derived rather than
  // sent, so there's one source of truth instead of two that can disagree.
  reaches?: string[];
  // Whether the walk ran at all. An inbound binding with no indexed handler
  // has nothing to walk from, and "can't tell" must not render as "reaches
  // nothing" — that's the more confident claim, and the wrong one.
  crossingKnown?: boolean;
}

export interface ServiceView {
  name: string;
  module?: string;
  root?: string;
  anchor?: TargetID; // the frame zoomed out from, carried up as the anchor
  anchorTitle?: string;
  inbound: Binding[];
  outbound: Binding[];
  // Why part of the view may be missing (usually an unresolvable proto root).
  warning?: string;
  // Outbound calls excluded because execution can't reach them from any
  // recognized entrypoint — reported so an empty column can be told apart
  // from a router unfold can't read.
  outboundUnreachable?: number;
  protoRoot?: string; // the shared proto repository currently configured
  repos?: RepoInfo[]; // present when a workspace of several repos is open
  // The manifest declares protoPaths that can't be resolved yet — the cue to
  // offer the picker rather than just reporting the problem.
  needsProtoRoot?: boolean;
}

export interface RepoInfo {
  alias: string;
  name: string;
  dir: string;
  primary?: boolean;
  indexed?: boolean; // its Go code is loaded; lazy repos start false
  error?: string;
}

// The answer to "open the implementation of this key". target is empty when
// the serving repo is known but its implementation couldn't be identified.
export interface Resolution {
  repo: string;
  service: string;
  target?: TargetID;
  title?: string;
  stale?: boolean;
  note?: string;
  candidates?: Candidate[];
}

// The L0 view. Services come from declarations so all are listed; edges need
// a service's code to have been read, so an un-indexed service shows no
// outgoing calls — which the view says rather than implying it calls nothing.
export interface PlatformView {
  services: PlatformService[];
  edges: PlatformEdge[];
  // The frame this view was zoomed out from, carried up so the platform level
  // marks what reaches it — the same anchor the service level uses.
  anchor?: TargetID;
  anchorTitle?: string;
}

export interface PlatformService {
  alias: string;
  name: string;
  dir: string;
  primary?: boolean;
  indexed?: boolean;
  error?: string;
  methods?: number; // RPCs it declares, known from protos alone
  reachesAnchor?: boolean; // holds the anchor, or calls an API leading to it
}

export interface PlatformEdge {
  from: string;
  to: string;
  kind: string;
  calls: PlatformCall[];
  reachesAnchor?: boolean;
}

export interface PlatformCall {
  key: string;
  reachesAnchor?: boolean;
  site?: TargetID;
  siteTitle?: string;
  file?: string;
  line?: number;
}

export interface TypeInfo {
  kind: string;
  name: string;
  type: string;
  definedAt?: string; // "<file>:<line>"
  doc?: string;
  targetId?: TargetID; // present when the symbol is a function we can open
  definition?: string; // expanded type shape (fields/methods), multi-line
  // Recognizers already matching this call site, built-in and configured.
  // The card offers to author a rule from the call; without these, that offer
  // reads as "nothing recognizes this" even when something does.
  rules?: string[];
  // The call under the pointer, as the rule evaluator sees it. Present when
  // the hovered symbol is the function being called.
  call?: CallFacts;
}

// What a rule can match on at one call site (mirrors model.CallFacts).
// Resolved by the type checker, so it's available for a callee this index
// doesn't hold — an interface method from a dependency, which is exactly the
// case the old "derive it from targetId" approach came up empty on.
export interface CallFacts {
  package?: string;
  recv?: string;
  recvPkg?: string;
  func?: string;
  args?: ArgFacts[];
}

export interface ArgFacts {
  // What the caller passed, and what the callee declares. They differ when a
  // parameter is an interface or `any` — which is when one of them is the
  // only useful one.
  type?: string;
  paramType?: string;
  value?: string; // constant-folded, when the argument has one
}

// A note anchored to a source location (mirrors internal/notes). Anchors
// live in file space, so a note renders in every frame containing its
// line(s) — function frames and whole-file frames alike.
export interface NoteAnchor {
  file: string;
  kind: "after-line" | "range" | "file-start" | "file-end";
  startLine?: number; // 1-based; after-line has startLine === endLine
  endLine?: number;
  snippet?: string; // anchored line's text at save time, for drift detection
}

export interface Note {
  id: string;
  anchor: NoteAnchor;
  text: string; // may contain [[SymbolName]] / [[file:path]] references
  createdAt?: string;
  updatedAt?: string;
}

// One recognizer, built-in or configured — what it is, whether it's on, where
// it came from, and how much it actually matched.
export interface RuleInfo {
  id: string;
  doc?: string;
  builtin?: boolean;
  enabled: boolean;
  source?: string;
  matches: number;
  // The rule as written. Absent for built-ins — their body is Go, and the
  // only editable thing about them is `enabled`.
  spec?: RuleSpec;
}

export interface RuleReport {
  rules: RuleInfo[];
  // Rules that were dropped or can never fire. Surfaced rather than swallowed:
  // a rule nobody is told about is exactly the failure the system avoids.
  problems?: string[];
}

// A configured rule, as saved. Mirrors internal/rules.Rule.
export interface RuleSpec {
  id: string;
  "//"?: string;
  enabled?: boolean;
  classifier?: boolean;
  match?: {
    args?: { index: number; type?: string; paramType?: string }[];
    package?: string;
    recv?: string;
    recvPkg?: string;
    recvOnly?: boolean;
    func?: string;
    minArgs?: number;
    calleeMatches?: { rule: string; depth?: number };
  };
  emit?: {
    role: "inbound" | "outbound";
    kind: string;
    key: string;
    handler?: string;
    confidence?: "declared" | "inferred";
  };
}
