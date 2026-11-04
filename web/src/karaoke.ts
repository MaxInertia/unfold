import type { SpokenStop } from "./speech";
import { callLine, resolveCue, type CueFrame } from "./cues";
import type { CallSite } from "./types";

// Karaoke: where the eye should be while a stop is narrated. The narration
// is prose about a function, not the function read out, so words don't
// map to lines on their own; the author maps them, with cues (see
// cues.ts), and a named call — [[Name]] — is a cue for that call's line.
// The highlight starts on the stop's anchor line, jumps when the
// narration reaches a cue, and stays there until the next. It never
// drifts: a line that nobody is talking about should not be lit.
// Pure, so the rules are testable.

export interface Waypoint {
  char: number; // index into the spoken text
  line: number; // 1-based file line
  end?: number; // inclusive, for a range cue
}

export type TimelineFrame = CueFrame & { calls?: CallSite[] };

export function buildTimeline(
  spoken: SpokenStop,
  frame: TimelineFrame,
): Waypoint[] {
  const points: Waypoint[] = [{ char: 0, line: frame.anchor }];
  for (const r of spoken.refs) {
    if (r.name.startsWith("file:")) continue;
    const line = callLine(r.name, frame);
    if (line !== null) points.push({ char: r.char, line });
  }
  for (const c of spoken.cues) {
    const t = resolveCue(c.cue, frame);
    if (t) points.push({ char: c.char, line: t.line, end: t.end });
  }
  // Cues and refs are each in text order; merged, keeping that order for
  // equal positions.
  return points.sort((a, b) => a.char - b.char);
}

// The highlight for a position in the narration: the latest waypoint
// reached.
export function lineAt(timeline: Waypoint[], char: number): Waypoint | null {
  let at: Waypoint | null = null;
  for (const w of timeline) {
    if (w.char > char) break;
    at = w;
  }
  return at;
}
