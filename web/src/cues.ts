import type { CallSite } from "./types";
import { findCall } from "./tourResolve";

// Cues: where the tour author wants the eye while a stop is narrated. A
// description is prose about a function, and nothing in the prose says
// which line it is about — so the author says, inline, with a cue token
// that is silent when spoken and a small mark when rendered:
//
//   [[line:42]]      a file line (1-based)
//   [[line:+3]]      the stop's anchor line plus 3 (the function's first
//                    line is +0; a file stop's `line` is +0)
//   [[line:42-48]]   a range, highlighted whole; "+2-5" is relative too
//   [[at:return nil, err]]  the first line of the stop's frame containing
//                    that text — survives edits above it
//
// The cursor jumps when the narration reaches the cue and stays until the
// next one. Without cues it sits on the anchor line; it never drifts.

export type Cue =
  | { kind: "line"; relative: boolean; start: number; end?: number }
  | { kind: "at"; text: string };

// A ref token is a cue when it uses one of the cue prefixes; the result is
// null for an ordinary [[Name]] or [[file:path]] reference, and "invalid"
// for a cue that doesn't parse, so it can be shown as such.
export function parseCue(ref: string): Cue | null | "invalid" {
  const m = ref.match(/^(line|at)\s*:(.*)$/s);
  if (!m) return null;
  const body = m[2].trim();
  if (m[1] === "at") return body ? { kind: "at", text: body } : "invalid";
  const r = body.match(/^(\+?)(\d+)(?:\s*-\s*(\d+))?$/);
  if (!r) return "invalid";
  const relative = r[1] === "+";
  const start = Number(r[2]);
  const end = r[3] === undefined ? undefined : Number(r[3]);
  if (!relative && start < 1) return "invalid";
  if (end !== undefined && end < start) return "invalid";
  return { kind: "line", relative, start, end };
}

export interface CueFrame {
  // The 1-based file line that is +0: a function's first line, or a file
  // stop's `line`.
  anchor: number;
  // The frame's source, when there is one; "at:" cues need it.
  source?: string;
}

export interface CueTarget {
  line: number; // 1-based file line
  end?: number; // inclusive, when the cue is a range
}

// Where a cue lands in file lines, or null when it can't (an "at:" with
// no match, or no source to search).
export function resolveCue(cue: Cue, frame: CueFrame): CueTarget | null {
  if (cue.kind === "line") {
    const base = cue.relative ? frame.anchor : 0;
    const line = base + cue.start;
    const end = cue.end === undefined ? undefined : base + cue.end;
    return { line, end };
  }
  if (frame.source === undefined) return null;
  const lines = frame.source.split("\n");
  const idx = lines.findIndex((l) => l.includes(cue.text));
  return idx < 0 ? null : { line: frame.anchor + idx };
}

// What a cue is called in the UI: "line 42", "lines 42–48", "+3", "at …".
export function cueLabel(cue: Cue): string {
  if (cue.kind === "at") return `at: ${cue.text}`;
  const n = (v: number) => (cue.relative ? `+${v}` : String(v));
  if (cue.end !== undefined) return `lines ${n(cue.start)}–${n(cue.end)}`;
  return `line ${n(cue.start)}`;
}

// The line of a call named in a description — the other thing that moves
// the cursor. Absolute, like a cue's.
export function callLine(
  name: string,
  frame: CueFrame & { calls?: CallSite[] },
): number | null {
  if (!frame.calls || frame.source === undefined) return null;
  const call = findCall(frame.calls, name);
  if (!call) return null;
  let line = 0;
  const stop = Math.min(call.spanStart, frame.source.length);
  for (let i = 0; i < stop; i++) if (frame.source.charCodeAt(i) === 10) line++;
  return frame.anchor + line;
}
