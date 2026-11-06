import type { CallSite, Frame, TraceEvent } from "./types";
import { pathKey, type FramePath, type FrameSlice } from "./viewState";

// Replaying a trace: turning "the events up to here" into what the view
// should show — which function is the root, which calls under it are
// unfolded, and which line the highlight is on. Pure, so the mapping from
// an event index to a view is testable without React or the network; the
// frames are injected as a lookup.

export interface StackEntry {
  fn: string;
  // The line the function is currently on: its declaration line when just
  // entered, then the last statement line recorded in it.
  line: number;
}

// What is known about a function's frame: the Frame, null when the index
// has no such function (a test function — unfold doesn't index _test.go —
// or a dependency), undefined when it hasn't been fetched yet.
export type FrameLookup = (fn: string) => Frame | null | undefined;

export interface Cursor {
  key: string; // pathKey of the frame the line is in
  file: string;
  line: number; // 1-based file line
}

export interface ReplayView {
  stack: StackEntry[];
  // The function shown as the root frame: the first on the stack the index
  // knows. Null while nothing placeable is executing (between top-level
  // calls, inside the test body itself).
  root: string | null;
  tree: FrameSlice;
  cursor: Cursor | null;
  // Functions on the stack the view can't show, deepest last: not indexed,
  // or reached through a call site that couldn't be matched. The cursor
  // waits on the call line while they run.
  unplaced: string[];
}

export const emptySlice: FrameSlice = { folds: [], expansions: {} };

// ---- the stack over time ----

// Folds one event into a goroutine's stack. Exported for the timeline's
// checkpoints; `stack` is mutated.
export function advance(stack: StackEntry[], ev: TraceEvent): void {
  switch (ev.kind) {
    case "enter":
      stack.push({ fn: ev.fn ?? "?", line: ev.line ?? 0 });
      break;
    case "line":
      if (stack.length > 0) stack[stack.length - 1].line = ev.line ?? 0;
      break;
    case "exit":
      stack.pop();
      break;
  }
}

// A goroutine's events with the stack reconstructible at any index. Stacks
// are snapshotted every `stride` events so a scrub to the far end replays a
// few hundred events, not the whole trace; stepping forward reuses the last
// answer.
export class Timeline {
  private checkpoints: StackEntry[][] = [];
  private last: { index: number; stack: StackEntry[] } | null = null;

  constructor(
    readonly events: TraceEvent[],
    private readonly stride = 512,
  ) {
    const stack: StackEntry[] = [];
    for (let i = 0; i < events.length; i++) {
      if (i % stride === 0) this.checkpoints.push(cloneStack(stack));
      advance(stack, events[i]);
    }
  }

  // The stack after events[0..index]; -1 is before anything ran.
  stackAt(index: number): StackEntry[] {
    if (index < 0 || this.events.length === 0) return [];
    index = Math.min(index, this.events.length - 1);
    let from: number;
    let stack: StackEntry[];
    if (
      this.last &&
      this.last.index <= index &&
      index - this.last.index < this.stride
    ) {
      from = this.last.index + 1;
      stack = cloneStack(this.last.stack);
    } else {
      const k = Math.floor(index / this.stride);
      from = k * this.stride;
      stack = cloneStack(this.checkpoints[k]);
    }
    for (let i = from; i <= index; i++) advance(stack, this.events[i]);
    this.last = { index, stack: cloneStack(stack) };
    return stack;
  }
}

function cloneStack(s: StackEntry[]): StackEntry[] {
  return s.map((e) => ({ ...e }));
}

// ---- placing the stack in the view ----

interface Placement {
  path: FramePath;
  frame: Frame;
}

// What the view shows for a stack. The first function the index knows is the
// root; each one above it is looked for among its parent's call sites on
// the line the parent was on when it was entered, and unfolds there. A
// function literal has no frame of its own but its lines are inside the
// function that declares it, so it is placed in that function's frame when
// that function is on the stack below it — a callback handed down and
// invoked deeper still highlights where it was written.
export function place(stack: StackEntry[], frames: FrameLookup): ReplayView {
  const placements: (Placement | null)[] = [];
  const unplaced: string[] = [];
  let root: string | null = null;
  for (let i = 0; i < stack.length; i++) {
    const e = stack[i];
    const base = closureBase(e.fn);
    if (base !== null) {
      let p: Placement | null = null;
      for (let j = i - 1; j >= 0; j--) {
        if (stack[j].fn === base && placements[j]) {
          p = placements[j];
          break;
        }
      }
      placements.push(p);
      if (!p) unplaced.push(e.fn);
      continue;
    }
    const frame = frames(e.fn);
    if (root === null) {
      if (frame) {
        root = e.fn;
        placements.push({ path: [], frame });
      } else {
        placements.push(null);
        unplaced.push(e.fn);
      }
      continue;
    }
    const parent = placements[i - 1];
    if (!parent || !frame) {
      placements.push(null);
      unplaced.push(e.fn);
      continue;
    }
    const site = findCallSite(parent.frame, stack[i - 1].line, e.fn);
    if (!site) {
      placements.push(null);
      unplaced.push(e.fn);
      continue;
    }
    placements.push({ path: [...parent.path, site], frame });
  }

  const tree: FrameSlice = { folds: [], expansions: {} };
  let cursor: Cursor | null = null;
  for (let i = 0; i < placements.length; i++) {
    const p = placements[i];
    if (!p) continue;
    insertPath(tree, p.path);
    cursor = { key: pathKey(p.path), file: p.frame.file, line: stack[i].line };
  }
  return { stack, root, tree, cursor, unplaced };
}

// "pkg.Outer.func1" and "pkg.Outer.func1.2" → "pkg.Outer"; anything else
// is not a literal.
export function closureBase(fn: string): string | null {
  const m = fn.match(/^(.*)\.func\d+(?:\.\d+)*$/);
  return m ? m[1] : null;
}

// The call site in `frame` that entered `fn`, preferring one on `line` (the
// statement the caller was on), then the nearest one after it — a call
// whose arguments span lines starts after the statement does — then the
// nearest before. A site that only references the function (a callback
// handed over) is taken last: it is where the function was named, which is
// still better than nowhere.
export function findCallSite(
  frame: Frame,
  line: number,
  fn: string,
): { callId: string; choice: number } | null {
  const idx = line - frame.startLine;
  let best: { call: CallSite; choice: number; rank: number } | null = null;
  for (const call of frame.calls) {
    const choice = choiceFor(call, fn);
    if (choice === null) continue;
    const at = lineForOffset(frame.source, call.spanStart);
    let rank: number;
    if (at === idx) rank = 0;
    else if (at > idx) rank = 1 + (at - idx) / 1e6;
    else rank = 2 + (idx - at) / 1e6;
    if (call.kind === "ref") rank += 10;
    if (!best || rank < best.rank) best = { call, choice, rank };
  }
  return best ? { callId: best.call.id, choice: best.choice } : null;
}

function choiceFor(call: CallSite, fn: string): number | null {
  if (call.targetId === fn) return 0;
  const i = call.candidates?.findIndex((c) => c.targetId === fn) ?? -1;
  return i >= 0 ? i : null;
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}

function insertPath(tree: FrameSlice, path: FramePath): void {
  let slice = tree;
  for (const step of path) {
    let child = slice.expansions[step.callId];
    if (!child || child.choice !== step.choice) {
      child = { folds: [], expansions: {}, choice: step.choice };
      slice.expansions[step.callId] = child;
    }
    slice = child;
  }
}

// ---- moving the view from one tree to the next ----

export type TreeOp =
  | { op: "expand"; path: FramePath; callId: string; choice: number }
  | { op: "collapse"; path: FramePath; callId: string };

// The expansions to open and close to get from `prev` to `next`, parents
// before children. One event moves the stack by one, so this is usually a
// single op; a scrub produces the whole difference at once.
export function diffTrees(
  prev: FrameSlice,
  next: FrameSlice,
  path: FramePath = [],
): TreeOp[] {
  const out: TreeOp[] = [];
  for (const callId of Object.keys(next.expansions)) {
    const n = next.expansions[callId];
    const p = prev.expansions[callId];
    const childPath: FramePath = [...path, { callId, choice: n.choice }];
    if (!p || p.choice !== n.choice) {
      out.push({ op: "expand", path, callId, choice: n.choice });
      out.push(...diffTrees(emptySlice, n, childPath));
    } else {
      out.push(...diffTrees(p, n, childPath));
    }
  }
  for (const callId of Object.keys(prev.expansions)) {
    if (!next.expansions[callId]) out.push({ op: "collapse", path, callId });
  }
  return out;
}

// The function name as a reader knows it: "Validate", "Store.List".
export function shortFn(fn: string): string {
  const base = closureBase(fn);
  if (base !== null) return shortFn(base) + fn.slice(base.length);
  const m = fn.match(/^\(\*?(.*)\)\.(.*)$/);
  if (m)
    return (
      m[1].slice(m[1].lastIndexOf("/") + 1).replace(/^[^.]*\./, "") + "." + m[2]
    );
  const tail = fn.slice(fn.lastIndexOf("/") + 1);
  return tail.slice(tail.indexOf(".") + 1);
}
