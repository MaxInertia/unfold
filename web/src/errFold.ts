import type { ErrReturn } from "./types";

// The plan for rendering a frame with "assume err == nil" on: which rows
// disappear, which `if` lines carry a glyph, and how many blocks folded.
export interface ErrFoldPlan {
  // 0-based line indices that produce no row at all.
  hidden: Set<number>;
  // `if` line index -> the block folded behind it. Only folded blocks are
  // here; an open block renders its lines like any other.
  glyphs: Map<number, ErrReturn>;
  // Blocks folded (the header count).
  folded: number;
}

const EMPTY: ErrFoldPlan = { hidden: new Set(), glyphs: new Map(), folded: 0 };

// planErrFolds decides which error-return blocks actually fold. `open` holds
// blocks the reader expanded one at a time (by their `if` line); `pinned`
// holds lines that must stay on screen — a tour stop, a note, an expanded
// call, the current selection. A block touching a pinned line stays open:
// folding it would hide the thing the reader was looking at, and the
// line-anchored cards hanging off it would have no row to render after.
//
// Only the lines after the `if` are hidden. The `if` line itself stays as
// the glyph's anchor, which also keeps its file line addressable — tour cues
// and stop cards point at file lines, so a visible row must never move.
export function planErrFolds(
  blocks: readonly ErrReturn[] | undefined,
  open: ReadonlySet<number>,
  pinned: ReadonlySet<number>,
): ErrFoldPlan {
  if (!blocks || blocks.length === 0) return EMPTY;
  const hidden = new Set<number>();
  const glyphs = new Map<number, ErrReturn>();
  for (const b of blocks) {
    if (b.endLine < b.startLine || open.has(b.startLine)) continue;
    let pin = false;
    for (let l = b.startLine + 1; l <= b.endLine && !pin; l++)
      if (pinned.has(l)) pin = true;
    if (pin) continue;
    glyphs.set(b.startLine, b);
    for (let l = b.startLine + 1; l <= b.endLine; l++) hidden.add(l);
  }
  return { hidden, glyphs, folded: glyphs.size };
}

// errCount words the header count: "1 err return folded" / "12 err returns
// folded".
export function errCountLabel(n: number): string {
  return `${n} err ${n === 1 ? "return" : "returns"} folded`;
}
