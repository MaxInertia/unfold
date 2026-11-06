import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import type { Element, ElementContent } from "hast";
import { fetchFlow } from "./api";
import { matches } from "./keybindings";
import type {
  CallID,
  Flow,
  FlowEdge,
  FlowSpan,
  Frame,
  TargetID,
} from "./types";
import {
  pathKey,
  useViewStore,
  type FramePath,
  type FrameSlice,
} from "./viewState";

// Follow a value: alt-click (or `v`, then click) a variable and every place
// its value goes lights up — through its own function, into the parameter of
// an unfolded callee, back out through a return into the caller's assignment.
//
// The state is one followed value and, per open frame, the spans to mark in
// it. It lives outside React's tree because the spans cross it: a value
// followed in a child frame has to light up in the parent, and props only
// flow the other way. Frames subscribe by their path key, the same key the
// view store uses, so a frame and its entry are matched by position in the
// view rather than by function — the same function open twice (recursion)
// gets an entry per occurrence.
//
// Each frame resolves its own entry: given a seed (an identifier in it the
// value arrives at), it asks the server for that identifier's flow and gets
// back the spans to mark plus the edges leading onward. Then it seeds its
// neighbours — the child a "param" edge enters, the parent an "arg" edge
// returns to — but only if they are open and have no entry yet, so the
// trail follows what is unfolded and terminates on its own.

interface Entry {
  spans: FlowSpan[];
  // Where to keep following from in this frame; null when the far end was an
  // expression with no single name. Resolved once per entry.
  seed: FlowSpan | null;
  resolved: boolean;
  pending: boolean;
  edges: FlowEdge[];
}

export interface FollowState {
  // `v` was pressed: the next click follows instead of expanding.
  armed: boolean;
  active: {
    name: string;
    kind: string;
    type: string;
    // The click that started it, so that one spelling can be outlined.
    origin: { key: string; offset: number };
  } | null;
  // Entries by frame path key.
  entries: Record<string, Entry>;
}

const idle: FollowState = Object.freeze({
  armed: false,
  active: null,
  entries: {},
});
let state: FollowState = idle;
// Bumped by every start/clear so a reply from a superseded follow is dropped
// rather than written into the one that replaced it.
let gen = 0;
const listeners = new Set<() => void>();

function set(next: FollowState) {
  state = next;
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useFollowState(): FollowState {
  return useSyncExternalStore(subscribe, () => state);
}

export function armFollow() {
  if (!state.armed) set({ ...state, armed: true });
}

export function clearFollow() {
  gen++;
  if (state !== idle) set(idle);
}

// Begin following the value at a frame offset. A click lands on a caret
// position, which for the last character of a name is the position *after*
// it; the server sees no identifier there, so the character before is tried
// too before giving up.
export function startFollow(key: string, targetId: TargetID, offset: number) {
  const my = ++gen;
  set({
    armed: false,
    active: null,
    entries: {
      [key]: {
        spans: [],
        seed: null,
        resolved: false,
        pending: true,
        edges: [],
      },
    },
  });
  // One step back and no further: walking on would find *some* value
  // somewhere earlier in the source, which is not what was clicked.
  const attempt = (off: number, retry: boolean): Promise<Flow | null> =>
    fetchFlow(targetId, off).then((flow) =>
      flow || !retry || off <= 0 ? flow : attempt(off - 1, false),
    );
  attempt(offset, true)
    .then((flow) => {
      if (my !== gen) return;
      if (!flow) {
        set(idle);
        return;
      }
      set({
        armed: false,
        active: {
          name: flow.name,
          kind: flow.kind,
          type: flow.type,
          origin: { key, offset },
        },
        entries: {
          [key]: {
            spans: flow.spans,
            seed: null,
            resolved: true,
            pending: false,
            edges: flow.edges,
          },
        },
      });
    })
    .catch(() => {
      if (my === gen) set(idle);
    });
}

// Give a frame an entry from an edge that leads into it. A frame that already
// has one keeps it: the edge back from a child to the parent it was seeded
// from would otherwise reseed the parent, and so on forever.
function seedEntry(key: string, spans: FlowSpan[], seed: FlowSpan | null) {
  if (!state.active || state.entries[key]) return;
  set({
    ...state,
    entries: {
      ...state.entries,
      [key]: { spans, seed, resolved: false, pending: false, edges: [] },
    },
  });
}

// Ask the server where the seeded identifier goes inside this frame, and
// merge the answer into the entry. A null answer (the seed wasn't a value
// after all) still marks the entry resolved so it isn't asked again.
function resolveEntry(key: string, targetId: TargetID, offset: number) {
  const entry = state.entries[key];
  if (!entry || entry.pending || entry.resolved) return;
  const my = gen;
  set({
    ...state,
    entries: { ...state.entries, [key]: { ...entry, pending: true } },
  });
  fetchFlow(targetId, offset)
    .then((flow) => {
      if (my !== gen) return;
      const cur = state.entries[key];
      if (!cur) return;
      set({
        ...state,
        entries: {
          ...state.entries,
          [key]: {
            ...cur,
            pending: false,
            resolved: true,
            spans: flow ? unionSpans(cur.spans, flow.spans) : cur.spans,
            edges: flow ? flow.edges : [],
          },
        },
      });
    })
    .catch(() => {
      if (my !== gen) return;
      const cur = state.entries[key];
      if (cur) {
        set({
          ...state,
          entries: {
            ...state.entries,
            [key]: { ...cur, pending: false, resolved: true },
          },
        });
      }
    });
}

export function unionSpans(a: FlowSpan[], b: FlowSpan[]): FlowSpan[] {
  const out = [...a];
  for (const s of b) {
    if (
      !out.some(
        (t) =>
          t.file === s.file &&
          t.line === s.line &&
          t.col === s.col &&
          t.len === s.len,
      )
    ) {
      out.push(s);
    }
  }
  return out;
}

// ---- per-frame hook ----

interface FrameWindow {
  file: string;
  startLine: number;
  endLine: number;
}

export interface MarkRange {
  start: number; // UTF-16 offsets within the line
  end: number;
  origin?: boolean;
}

// What a frame needs from the follow state: which characters of which lines
// to mark, and a click handler. `children` are the frames expanded inside
// this one, by call id — how an edge into a callee finds the frame it enters.
export function useFollowInFrame(
  frame: Frame,
  path: FramePath,
  slice: FrameSlice,
  children: Map<CallID, Frame>,
) {
  const st = useFollowState();
  const key = pathKey(path);
  const entry = st.entries[key];
  const lineStarts = useMemo(
    () => computeLineStarts(frame.source),
    [frame.source],
  );
  // The path is rebuilt by the parent on every render; its key is what
  // identifies it, so the effects below key on that and read the path lazily.
  const pathRef = useRef(path);
  pathRef.current = path;

  // Resolve: an entry that arrived with a seed asks where the seed goes.
  useEffect(() => {
    if (!entry || entry.resolved || entry.pending || !entry.seed) return;
    const off = spanOffset(frame, lineStarts, entry.seed);
    if (off == null) return;
    resolveEntry(key, frame.id, off);
  }, [entry, frame, lineStarts, key]);

  // Propagate: a resolved entry seeds the open neighbours its edges lead to.
  useEffect(() => {
    if (!entry?.resolved) return;
    const p = pathRef.current;
    for (const e of entry.edges) {
      if (e.kind === "arg" || e.kind === "ret") {
        // Up: the caller is the frame this one is expanded from, if the edge
        // is about that very call site.
        if (p.length === 0 || p[p.length - 1].callId !== e.callId) continue;
        seedEntry(pathKey(p.slice(0, -1)), e.spans, e.seed ?? null);
      } else {
        // Down: the callee is open at this call, and is the implementation
        // the edge is about (an interface call may have opened another).
        const exp = slice.expansions[e.callId];
        const child = children.get(e.callId);
        if (!exp || !child || child.id !== e.targetId) continue;
        seedEntry(
          pathKey([...p, { callId: e.callId, choice: exp.choice }]),
          e.spans,
          e.seed ?? null,
        );
      }
    }
  }, [entry, slice.expansions, children, key]);

  const marks = useMemo(() => {
    if (!entry || !st.active) return null;
    const o = st.active.origin;
    const origin =
      o.key === key ? offsetToLineCol(lineStarts, o.offset) : undefined;
    const m = marksForFrame(frame, entry.spans, origin);
    return m.size ? m : null;
  }, [entry, st.active, key, lineStarts, frame]);

  const decorateLine = useMemo(
    () =>
      marks
        ? (idx: number, line: Element) => {
            const r = marks.get(idx);
            return r ? markLine(line, r) : line;
          }
        : undefined,
    [marks],
  );

  return {
    armed: st.armed,
    active: !!st.active,
    decorateLine,
    lineClass: marks
      ? (idx: number) => (marks.has(idx) ? "line-row--follow" : undefined)
      : undefined,
    // Begin following at an offset in this frame's source.
    start: (offset: number) => startFollow(key, frame.id, offset),
  };
}

// ---- pure helpers (tested in follow.test.ts) ----

export function computeLineStarts(source: string): number[] {
  const starts = [0];
  for (let i = 0; i < source.length; i++) {
    if (source.charCodeAt(i) === 10) starts.push(i + 1);
  }
  return starts;
}

// The offset into a frame's source where a file span starts, or null when the
// span is outside the frame.
export function spanOffset(
  frame: FrameWindow,
  lineStarts: number[],
  span: FlowSpan,
): number | null {
  if (span.file !== frame.file) return null;
  const idx = span.line - frame.startLine;
  if (idx < 0 || idx >= lineStarts.length) return null;
  return lineStarts[idx] + span.col - 1;
}

export function offsetToLineCol(
  lineStarts: number[],
  offset: number,
): { lineIdx: number; col: number } {
  let lo = 0;
  let hi = lineStarts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (lineStarts[mid] <= offset) lo = mid;
    else hi = mid - 1;
  }
  return { lineIdx: lo, col: offset - lineStarts[lo] };
}

// The marks for one frame: file spans that fall inside its window, as ranges
// within each of its lines, overlapping ones merged. The range under `origin`
// (a position in the frame) is flagged so it can be drawn differently.
export function marksForFrame(
  frame: FrameWindow,
  spans: FlowSpan[],
  origin?: { lineIdx: number; col: number },
): Map<number, MarkRange[]> {
  const byLine = new Map<number, MarkRange[]>();
  for (const s of spans) {
    if (
      s.file !== frame.file ||
      s.line < frame.startLine ||
      s.line > frame.endLine
    )
      continue;
    const idx = s.line - frame.startLine;
    const start = s.col - 1;
    const r: MarkRange = { start, end: start + s.len };
    if (
      origin &&
      origin.lineIdx === idx &&
      origin.col >= r.start &&
      origin.col <= r.end
    ) {
      r.origin = true;
    }
    byLine.set(idx, [...(byLine.get(idx) ?? []), r]);
  }
  for (const [idx, ranges] of byLine) byLine.set(idx, mergeRanges(ranges));
  return byLine;
}

export function mergeRanges(ranges: MarkRange[]): MarkRange[] {
  const sorted = [...ranges].sort((a, b) => a.start - b.start || a.end - b.end);
  const out: MarkRange[] = [];
  for (const r of sorted) {
    const last = out[out.length - 1];
    if (last && r.start < last.end) {
      last.end = Math.max(last.end, r.end);
      if (r.origin) last.origin = true;
    } else {
      out.push({ ...r });
    }
  }
  return out;
}

// Wrap the characters of a highlighted line that fall in `ranges` in <mark>
// elements. The line is shiki's: a span of token spans, each holding text,
// with call-site spans possibly nested in between. Text nodes are split at
// range boundaries and the covered pieces wrapped; elements are rebuilt with
// their rewritten children, so a mark is always innermost and the call-site
// walker above still finds its spans. Offsets are UTF-16 within the line,
// which is what a JavaScript string's length counts.
export function markLine(line: Element, ranges: MarkRange[]): Element {
  if (ranges.length === 0) return line;
  const cursor = { pos: 0 };
  return rewrite(line, ranges, cursor) as Element;
}

function rewrite(
  node: ElementContent,
  ranges: MarkRange[],
  cursor: { pos: number },
): ElementContent | ElementContent[] {
  if (node.type === "text") {
    const start = cursor.pos;
    const end = start + node.value.length;
    cursor.pos = end;
    const hits = ranges.filter((r) => r.start < end && r.end > start);
    if (hits.length === 0) return node;
    const out: ElementContent[] = [];
    let at = start;
    for (const r of hits) {
      const from = Math.max(r.start, at);
      const to = Math.min(r.end, end);
      if (from > at)
        out.push({
          type: "text",
          value: node.value.slice(at - start, from - start),
        });
      out.push({
        type: "element",
        tagName: "mark",
        properties: {
          className: [
            "follow-mark",
            ...(r.origin ? ["follow-mark--origin"] : []),
          ],
        },
        children: [
          { type: "text", value: node.value.slice(from - start, to - start) },
        ],
      });
      at = to;
    }
    if (at < end)
      out.push({ type: "text", value: node.value.slice(at - start) });
    return out;
  }
  if (node.type === "element") {
    const children: ElementContent[] = [];
    for (const c of node.children) {
      const r = rewrite(c, ranges, cursor);
      if (Array.isArray(r)) children.push(...r);
      else children.push(r);
    }
    return { ...node, children };
  }
  return node;
}

// ---- the pinned label and the keys ----

// Names what is being followed, in the hover card's style, parked in a corner.
// Also owns the keys: `v` arms a follow for the next click, esc clears. One
// per view — the root frame mounts it — so the listeners exist once.
export function FollowLabel() {
  const st = useFollowState();
  const symbol = useViewStore().symbol;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (matches("follow.arm", e)) {
        // `v` is a letter people type; unlike the tour keys it never fires
        // from a text field, empty or not — the search box has focus when the
        // page opens, and "v" there is the start of a search.
        if (isTypingTarget(e.target)) return;
        armFollow();
      } else if (matches("ui.dismiss", e) && (st.active || st.armed)) {
        // Esc clears from anywhere, the search box included: the marks are on
        // the page, not in the field.
        clearFollow();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [st.active, st.armed]);

  // A new root is a new reading; the followed value belonged to the old one.
  const firstSymbol = useRef(symbol);
  useEffect(() => {
    if (firstSymbol.current !== symbol) clearFollow();
    firstSymbol.current = symbol;
  }, [symbol]);

  if (!st.active) {
    if (!st.armed) return null;
    return (
      <div className="type-card follow-label" role="status">
        <span className="follow-label-hint">
          click a variable to follow it · esc to cancel
        </span>
      </div>
    );
  }
  const entries = Object.values(st.entries);
  const uses = entries.reduce((n, e) => n + e.spans.length, 0);
  const frames = entries.filter((e) => e.spans.length > 0).length;
  return (
    <div className="type-card follow-label" role="status">
      <span className="follow-label-mark" aria-hidden="true" />
      <span className="type-card-kind">{st.active.kind}</span>
      <span className="type-card-name">{st.active.name}</span>
      <span className="type-card-type" title={st.active.type}>
        {st.active.type}
      </span>
      <span className="follow-label-count">
        {uses} {uses === 1 ? "use" : "uses"} · {frames}{" "}
        {frames === 1 ? "frame" : "frames"}
      </span>
      <button
        type="button"
        className="follow-label-close"
        onClick={clearFollow}
        title="stop following (esc)"
      >
        ×
      </button>
    </div>
  );
}

function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toLowerCase();
  return (
    tag === "input" ||
    tag === "textarea" ||
    tag === "select" ||
    !!el.isContentEditable
  );
}
