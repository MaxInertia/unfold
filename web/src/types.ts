// Mirrors Frame and CallSite in internal/indexer/indexer.go.

export type TargetID = string;
export type CallID = string;
export type CallKind = "direct" | "interface" | "indirect" | "fanout";

export interface CallSite {
  id: CallID;
  spanStart: number; // byte offsets in Frame.source
  spanEnd: number;
  displayName: string;
  kind: CallKind;
  targetId?: TargetID; // present for direct calls
  candidates?: Candidate[]; // present for interface calls with known impls
  goroutine?: boolean; // call is launched with the `go` keyword
  receivers?: Receiver[]; // present for fan-out calls (all of them run)
  fanoutKind?: string; // e.g. "subscribers"
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
}

export interface TypeInfo {
  kind: string;
  name: string;
  type: string;
  definedAt?: string; // "<file>:<line>"
  doc?: string;
  targetId?: TargetID; // present when the symbol is a function we can open
}
