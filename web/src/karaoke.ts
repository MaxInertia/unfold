import type { SpokenStop } from "./speech";
import { findCall } from "./tourResolve";
import type { CallSite } from "./types";

// Karaoke: where the eye should be while a stop is narrated. The narration
// is prose about a function, not the function read out, so the mapping
// from words to lines is by waypoint: the highlight starts at the
// function's first line, jumps to a call when the description names it,
// and otherwise drifts down the body at the pace of the speech, reaching
// the last line as the narration ends. Pure, so the rules are testable.

export interface Waypoint {
  char: number; // index into the spoken text
  line: number; // 0-based line within the frame
}

export interface TimelineFrame {
  source: string;
  calls: CallSite[];
  lineCount: number; // lines in the frame's source
}

export function buildTimeline(spoken: SpokenStop, frame: TimelineFrame): Waypoint[] {
  const last = Math.max(0, frame.lineCount - 1);
  const points: Waypoint[] = [{ char: 0, line: 0 }];
  for (const r of spoken.refs) {
    if (r.name.startsWith("file:")) continue;
    const call = findCall(frame.calls, r.name);
    if (!call) continue;
    points.push({ char: r.char, line: lineForOffset(frame.source, call.spanStart) });
  }
  points.push({ char: spoken.text.length, line: last });
  return points;
}

// The line for a position in the narration: the latest waypoint's line
// when one was just named, otherwise a linear drift toward the next one.
// A named call holds the highlight for a moment (`hold` characters) before
// the drift resumes, so the eye has time to land.
export function lineAt(timeline: Waypoint[], char: number, hold = 24): number {
  if (timeline.length === 0) return 0;
  let i = 0;
  while (i + 1 < timeline.length && timeline[i + 1].char <= char) i++;
  const from = timeline[i];
  const to = timeline[i + 1];
  if (!to) return from.line;
  const start = i === 0 ? from.char : from.char + hold;
  if (char <= start) return from.line;
  const span = Math.max(1, to.char - start);
  const t = Math.min(1, (char - start) / span);
  return Math.round(from.line + (to.line - from.line) * t);
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}
