// Mirrors Frame and CallSite in internal/indexer/indexer.go.

export type TargetID = string;
export type CallID = string;
export type CallKind = "direct" | "interface" | "indirect";

export interface CallSite {
  id: CallID;
  spanStart: number; // byte offsets in Frame.source
  spanEnd: number;
  displayName: string;
  kind: CallKind;
  targetId?: TargetID; // present for direct calls
}

export interface Frame {
  id: TargetID;
  file: string;
  language: string; // "go"
  startLine: number;
  endLine: number;
  source: string;
  calls: CallSite[];
}

export interface SearchResult {
  targetId: TargetID;
  label: string;
  file: string;
  line: number;
}
