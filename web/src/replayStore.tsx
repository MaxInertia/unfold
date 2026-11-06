import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { fetchBodyByTarget, fetchTrace, fetchTraces } from "./api";
import {
  Timeline,
  closureBase,
  diffTrees,
  emptySlice,
  place,
  type Cursor,
  type ReplayView,
} from "./replay";
import type { Frame, Trace, TraceEvent, TraceSummary } from "./types";
import {
  pathKey,
  useViewStore,
  type FramePath,
  type FrameSlice,
} from "./viewState";

// Replay: a recorded run (see internal/tracer) walked through the unfolded
// code. The server holds the traces; this store holds the list, the one
// being replayed, and the reader's position in it — the event index on one
// goroutine — plus the frames the placement needs, fetched once per trace.
//
// Same subscribe shape as tours/notes/settings.

export interface ReplayState {
  loaded: boolean;
  traces: TraceSummary[];
  dir: string;
  // The trace being replayed, and the goroutine of it. A trace with several
  // goroutines is replayed one at a time: the view is one stack.
  active: Trace | null;
  loading: string | null; // name being fetched
  error: string | null;
  g: number | null;
  // Position: the index into the goroutine's events after which the view is
  // shown. -1 is "before anything ran".
  index: number;
  playing: boolean;
  speed: number; // events per second
  // Bumped as frames arrive, so the placement is redone with what is known.
  framesVersion: number;
  // Where the highlight is, for the frame that owns that line to mark it,
  // and the whole placement it came from, for the panel.
  cursor: Cursor | null;
  view: ReplayView | null;
}

let state: ReplayState = {
  loaded: false,
  traces: [],
  dir: "",
  active: null,
  loading: null,
  error: null,
  g: null,
  index: -1,
  playing: false,
  speed: 5,
  framesVersion: 0,
  cursor: null,
  view: null,
};
const listeners = new Set<() => void>();

// The goroutine's events and the stack over them, rebuilt when the trace or
// goroutine changes. Module-level rather than in state: they are derived,
// and the timeline caches its last answer.
let events: TraceEvent[] = [];
let timeline: Timeline | null = null;
// Frames by function name: a Frame, null when the server has none (not
// indexed), absent while in flight.
const frames = new Map<string, Frame | null>();
// Which selection the in-flight fetches belong to; results for an earlier
// trace are dropped.
let generation = 0;

function set(patch: Partial<ReplayState>) {
  state = { ...state, ...patch };
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useReplay(): ReplayState {
  return useSyncExternalStore(
    subscribe,
    () => state,
    () => state,
  );
}

export const SPEEDS = [1, 2, 5, 10, 25, 50];

// ---- loading ----

export function loadTraces(): Promise<void> {
  return fetchTraces()
    .then(({ traces, dir }) => set({ loaded: true, traces, dir, error: null }))
    .catch((e: Error) => set({ loaded: true, error: e.message }));
}

// Pick a trace (null clears). The goroutine defaults to the one the test
// ran on — the first to enter a Test function — and the position to the
// start.
export function selectTrace(name: string | null) {
  generation++;
  stopPlaying();
  frames.clear();
  if (name === null) {
    events = [];
    timeline = null;
    set({
      active: null,
      loading: null,
      g: null,
      index: -1,
      cursor: null,
      error: null,
    });
    return;
  }
  const gen = generation;
  set({ loading: name, error: null });
  fetchTrace(name)
    .then((t) => {
      if (gen !== generation) return;
      const g = defaultGoroutine(t.events);
      setEvents(t.events, g);
      set({ active: t, loading: null, g, index: -1, cursor: null });
      prefetchFrames(gen);
    })
    .catch((e: Error) => {
      if (gen === generation) set({ loading: null, error: e.message });
    });
}

export function selectGoroutine(g: number) {
  if (!state.active || g === state.g) return;
  stopPlaying();
  setEvents(state.active.events, g);
  set({ g, index: -1, cursor: null });
  prefetchFrames(generation);
}

function setEvents(all: TraceEvent[], g: number) {
  events = all.filter((e) => e.g === g);
  timeline = new Timeline(events);
}

function defaultGoroutine(all: TraceEvent[]): number {
  const test = all.find(
    (e) => e.kind === "enter" && /(^|\.)Test[^.]*$/.test(e.fn ?? ""),
  );
  return (test ?? all[0])?.g ?? 0;
}

// Every function the goroutine enters, fetched by its full name — the id
// the index keys frames by. A literal is fetched as the function declaring
// it. Capped: a trace can enter thousands of distinct functions, and the
// ones past the cap are simply shown as not placeable.
const FRAME_CAP = 400;

function prefetchFrames(gen: number) {
  const want = new Set<string>();
  for (const e of events) {
    if (e.kind !== "enter" || !e.fn) continue;
    want.add(closureBase(e.fn) ?? e.fn);
    if (want.size >= FRAME_CAP) break;
  }
  for (const fn of want) {
    if (frames.has(fn)) continue;
    fetchBodyByTarget(fn)
      .then((f) => {
        if (gen !== generation) return;
        frames.set(fn, f);
        set({ framesVersion: state.framesVersion + 1 });
      })
      .catch(() => {
        if (gen !== generation) return;
        frames.set(fn, null);
        set({ framesVersion: state.framesVersion + 1 });
      });
  }
}

const lookup = (fn: string) => frames.get(fn);

// ---- position ----

export function eventCount(): number {
  return events.length;
}

export function eventAt(i: number): TraceEvent | undefined {
  return events[i];
}

export function seek(i: number) {
  if (!timeline) return;
  const clamped = Math.max(-1, Math.min(i, events.length - 1));
  if (clamped === state.index) return;
  set({ index: clamped });
}

export function step(delta: number) {
  if (!state.active) return;
  // Stepping by hand takes over from playback.
  if (state.playing) stopPlaying();
  seek(state.index + delta);
}

export function setSpeed(speed: number) {
  set({ speed });
}

let timer = 0;

export function play() {
  if (!state.active || state.playing) return;
  // Play from the end starts over.
  if (state.index >= events.length - 1) set({ index: -1 });
  set({ playing: true });
  schedule();
}

function schedule() {
  window.clearTimeout(timer);
  timer = window.setTimeout(() => {
    if (!state.playing) return;
    if (state.index >= events.length - 1) {
      set({ playing: false });
      return;
    }
    set({ index: state.index + 1 });
    schedule();
  }, 1000 / state.speed);
}

export function stopPlaying() {
  window.clearTimeout(timer);
  if (state.playing) set({ playing: false });
}

export function togglePlay() {
  if (state.playing) stopPlaying();
  else play();
}

// ---- driving the view ----

// Mounted once (in App). Places the stack at the current index and moves
// the view to match: a new root replaces the view (one history entry, as
// opening a symbol is), a change under the same root is expands and
// collapses (which replace, like clicking them would). The cursor goes to
// the store for the owning frame to mark.
export function useReplayDriver(): ReplayView | null {
  const store = useViewStore();
  const { active, g, index, framesVersion } = useReplay();
  const view = useMemo<ReplayView | null>(() => {
    if (!active || !timeline) return null;
    return place(timeline.stackAt(index), lookup);
    // framesVersion is what changes the lookup's answers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, g, index, framesVersion]);

  // What the driver last applied, so the next view is a diff against it
  // rather than against whatever the reader may have clicked meanwhile —
  // the replay owns the frames it opened, not the rest of the view.
  const applied = useRef<{ root: string | null; tree: FrameSlice }>({
    root: null,
    tree: emptySlice,
  });

  useEffect(() => {
    if (!view) {
      applied.current = { root: null, tree: emptySlice };
      if (state.cursor || state.view) set({ cursor: null, view: null });
      return;
    }
    if (view.root !== null) {
      if (view.root !== applied.current.root) {
        store.setView(view.root, view.tree);
      } else {
        for (const op of diffTrees(applied.current.tree, view.tree)) {
          if (op.op === "expand") store.expand(op.path, op.callId, op.choice);
          else store.collapse(op.path, op.callId);
        }
      }
      applied.current = { root: view.root, tree: view.tree };
    }
    // Between placeable calls the view is left where it was; only the mark
    // goes away.
    const c = view.cursor;
    const same =
      (c === null && state.cursor === null) ||
      (c !== null &&
        state.cursor !== null &&
        c.key === state.cursor.key &&
        c.file === state.cursor.file &&
        c.line === state.cursor.line);
    set(same ? { view } : { cursor: c, view });
  }, [view, store]);

  return view;
}

// Marks the line the replay is on when it falls in this frame. Each Frame
// calls it with its own path; the one whose key the cursor names does the
// marking. The row may not exist yet — the frame was just expanded and is
// still fetching — so the mark is retried briefly rather than skipped. The
// view scrolls only when the line has left the middle band: a highlight
// that drags the scroll position on every event is unreadable.
export function useReplayCursor(frame: Frame, path: FramePath) {
  const cursor = useReplay().cursor;
  const key = pathKey(path);
  useEffect(() => {
    if (!cursor || cursor.key !== key || cursor.file !== frame.file) return;
    const idx = cursor.line - frame.startLine;
    if (idx < 0 || idx > frame.endLine - frame.startLine) return;
    let timer = 0;
    let tries = 0;
    let marked: HTMLElement | null = null;
    const attempt = () => {
      const frameEl = document.querySelector<HTMLElement>(
        `[data-frame-key="${cssEscape(key)}"]`,
      );
      const row = frameEl
        ? [
            ...frameEl.querySelectorAll<HTMLElement>(
              `[data-line-idx="${idx}"]`,
            ),
          ].find((r) => r.closest("[data-frame-key]") === frameEl)
        : undefined;
      if (row) {
        row.classList.add("line-row--replay");
        marked = row;
        const rect = row.getBoundingClientRect();
        const h = window.innerHeight;
        if (rect.top < h * 0.15 || rect.bottom > h * 0.85)
          row.scrollIntoView({ block: "center" });
        return;
      }
      if (++tries < 30) timer = window.setTimeout(attempt, 100);
    };
    attempt();
    return () => {
      window.clearTimeout(timer);
      marked?.classList.remove("line-row--replay");
    };
  }, [cursor, key, frame.file, frame.startLine, frame.endLine]);
}

function cssEscape(s: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function")
    return CSS.escape(s);
  return s.replace(/["\\]/g, "\\$&");
}
