import { useEffect, useMemo, useSyncExternalStore } from "react";
import { fetchBodyByCall, fetchFiles, fetchSymbol, fetchTours } from "./api";
import { resolveCue, type Cue } from "./cues";
import {
  buildTimeline,
  lineAt,
  type TimelineFrame,
  type Waypoint,
} from "./karaoke";
import { currentSettings } from "./settings";
import { prefetchSpeech, speak, stopSpeaking, stopSpeech } from "./speech";
import { resolveStop, type ResolvedStop } from "./tourResolve";
import type { Tour, TourStop } from "./types";
import { useViewStore } from "./viewState";

// Tours: ordered reading lists, read from the project's .unfold/tours/ via
// /api/tours. The server is the source of truth — the files are written by
// agents and people, and a "tours" event on /api/events says when one
// changed — so this store is a cache plus the one piece of state that is
// the reader's own: which tour they are on, and which stop.
//
// Same subscribe shape as bookmarks/notes/settings.

export interface TourState {
  loaded: boolean;
  tours: Tour[];
  dir: string;
  // The tour being followed, by name, and the stop the reader is on. -1 is
  // "not started": the tour is picked but nothing has been opened yet, so
  // the first "next" lands on stop 0 rather than skipping it.
  active: string | null;
  index: number;
  // Bumped whenever a stop should be opened in the view. Going to the stop
  // you are already on is a request to open it again, so it can't be keyed
  // on the index alone.
  openSeq: number;
  // What each stop of the active tour resolved to, keyed by stop position.
  resolved: Record<number, ResolvedStop | "pending">;
  // A line the root frame should scroll to once it shows that file.
  focus: { file: string; line: number; seq: number } | null;
  // Karaoke: the tour is being played through — each stop narrated, the
  // next opened when the narration ends.
  playing: boolean;
  // Where the narration is right now, as a line in a file, for the root
  // frame to highlight and keep in view. Null when nothing is narrating.
  cursor: { file: string; line: number; end?: number } | null;
}

let state: TourState = {
  loaded: false,
  tours: [],
  dir: "",
  active: null,
  index: -1,
  openSeq: 0,
  resolved: {},
  focus: null,
  playing: false,
  cursor: null,
};
const listeners = new Set<() => void>();
// The index revision the current resolutions were made under; a rebuild
// changes target ids, so they are redone when it moves.
let revision = 0;
// Resolution generation: results from an earlier tour pick or revision are
// dropped rather than written over newer ones.
let generation = 0;
const inflight = new Map<number, Promise<ResolvedStop>>();

function set(patch: Partial<TourState>) {
  state = { ...state, ...patch };
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useTours(): TourState {
  return useSyncExternalStore(
    subscribe,
    () => state,
    () => state,
  );
}

export function activeTour(s: TourState = state): Tour | null {
  return s.tours.find((t) => t.name === s.active) ?? null;
}

// ---- persistence of the reader's position ----
//
// Keyed by the tours directory, which is per project, so picking up where
// you left off doesn't carry across repositories.

function positionKey(): string {
  return `unfold.tour.${state.dir}`;
}

function savePosition() {
  if (!state.dir) return;
  try {
    localStorage.setItem(
      positionKey(),
      JSON.stringify({ active: state.active, index: state.index }),
    );
  } catch {
    /* unavailable — position just doesn't survive the tab */
  }
}

function loadPosition(): { active: string | null; index: number } | null {
  if (!state.dir) return null;
  try {
    const raw = localStorage.getItem(positionKey());
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return typeof parsed?.index === "number" ? parsed : null;
  } catch {
    return null;
  }
}

// ---- loading ----

let loadedOnce = false;

// Fetch the tour list. Called on boot and again on every "tours" event. The
// reader's pick survives a reload when the tour still exists; a tour that
// vanished from disk drops the pick rather than pointing at nothing.
export function loadTours(): Promise<void> {
  return fetchTours()
    .then(({ tours, dir }) => {
      let { active, index } = state;
      if (!loadedOnce) {
        loadedOnce = true;
        set({ dir });
        const saved = loadPosition();
        if (saved) ({ active, index } = saved);
      }
      const stillThere =
        active !== null && tours.some((t) => t.name === active);
      if (!stillThere) {
        // One tour and nothing picked: it's the tour. Picking is not
        // opening, so the view is untouched.
        active = tours.length === 1 ? tours[0].name : null;
        index = -1;
      }
      const before = activeTour();
      set({ loaded: true, tours, dir, active, index });
      const after = activeTour();
      // Re-resolve when the active tour changed identity or content; a
      // rewrite of the file is a new updatedAt.
      if (!after) {
        set({ resolved: {} });
      } else if (
        !before ||
        before.name !== after.name ||
        before.updatedAt !== after.updatedAt
      ) {
        if (index >= after.stops.length) set({ index: -1 });
        resolveAll();
      }
      savePosition();
    })
    .catch(() => {
      set({ loaded: true });
    });
}

// A rebuilt index invalidates every target id a stop resolved to.
export function setTourRevision(n: number) {
  if (n === revision) return;
  revision = n;
  if (activeTour()) resolveAll();
}

// ---- resolution ----

const resolvers = {
  fetchSymbol,
  fetchFiles,
  fetchBody: fetchBodyByCall,
};

function resolveAll() {
  const tour = activeTour();
  generation++;
  inflight.clear();
  if (!tour) {
    set({ resolved: {} });
    return;
  }
  const pending: Record<number, "pending"> = {};
  tour.stops.forEach((_, i) => (pending[i] = "pending"));
  set({ resolved: pending });
  tour.stops.forEach((stop, i) => void ensureResolved(i, stop));
}

function ensureResolved(i: number, stop: TourStop): Promise<ResolvedStop> {
  const existing = state.resolved[i];
  if (existing && existing !== "pending") return Promise.resolve(existing);
  let p = inflight.get(i);
  if (!p) {
    const gen = generation;
    p = resolveStop(stop, resolvers).then((r) => {
      if (gen === generation) set({ resolved: { ...state.resolved, [i]: r } });
      return r;
    });
    inflight.set(i, p);
  }
  return p;
}

// ---- navigation ----

export function selectTour(name: string | null) {
  if (name === state.active) return;
  stopTour();
  set({ active: name, index: -1 });
  resolveAll();
  savePosition();
}

export function goToStop(i: number) {
  const tour = activeTour();
  if (!tour || i < 0 || i >= tour.stops.length) return;
  set({ index: i, openSeq: state.openSeq + 1 });
  savePosition();
  // Read the stop as it opens, when asked to. Spoken here rather than in
  // the driver: the text is known without waiting on the resolution, and
  // an unresolved stop still has a description worth hearing.
  stopSpeaking();
  if (state.playing || currentSettings().tourReadAloud) speakStop(i);
}

// The identity a stop speaks under, so a play button can tell "this one
// is playing" from "something else is".
export function speechKey(i: number): string {
  return `${state.active}#${i}`;
}

// Read stop i aloud; pressing it again while it plays stops it. While the
// narration runs, the cursor follows it through the stop's frame — see
// karaoke.ts for how words map to lines — and when the tour is being
// played through, the end of one stop opens the next.
export function speakStop(i: number) {
  const tour = activeTour();
  const stop = tour?.stops[i];
  if (!tour || !stop) return;
  const spoken = stopSpeech(stop, i, tour.stops.length);
  const key = speechKey(i);
  // The timeline needs the resolved frame; it may still be in flight when
  // the narration starts, so it's built on first use rather than up front.
  let timeline: Waypoint[] | null | undefined;
  const onProgress = (char: number) => {
    if (timeline === undefined) {
      const r = state.resolved[i];
      if (!r || r === "pending") return; // not yet — try on the next word
      timeline =
        r.kind === "unresolved" ? null : buildTimeline(spoken, cueFrame(r));
    }
    if (!timeline) return;
    const r = state.resolved[i];
    if (!r || r === "pending" || r.kind === "unresolved") return;
    const at = lineAt(timeline, char);
    if (!at) return;
    const c = state.cursor;
    if (c?.file !== r.file || c.line !== at.line || c.end !== at.end)
      set({ cursor: { file: r.file, line: at.line, end: at.end } });
  };
  const rate = currentSettings().tourSpeechRate || 1;
  // The next stop's audio, fetched now so "next" plays without a gap.
  const next = tour.stops[i + 1];
  if (next) prefetchSpeech(stopSpeech(next, i + 1, tour.stops.length).text);
  speak(key, spoken.text, { rate, onProgress }).then((natural) => {
    if (state.cursor) set({ cursor: null });
    if (!natural || !state.playing || state.index !== i) return;
    if (i + 1 >= tour.stops.length) {
      set({ playing: false });
      return;
    }
    // A beat between stops, shorter at higher speeds.
    window.setTimeout(() => {
      if (state.playing && state.index === i) goToStop(i + 1);
    }, 700 / rate);
  });
}

// The frame a stop's cues resolve against: its anchor line, and the source
// and calls to search. A symbol's source starts on its anchor; a file
// stop's is the whole file, so "+N" counts from the stop's line while
// "at:" and [[Name]] count from line 1.
export function cueFrame(
  r: Exclude<ResolvedStop, "unresolved" | { kind: "unresolved" }>,
): TimelineFrame {
  if (r.kind === "symbol")
    return { anchor: r.line, source: r.frame.source, calls: r.frame.calls };
  return {
    anchor: r.line,
    source: r.frame?.source,
    sourceLine: 1,
    calls: r.frame?.calls,
  };
}

// Put the narration highlight on a line of stop i's file by hand — a
// clicked cue — whether or not anything is being read.
export function showCue(i: number, cue: Cue) {
  const r = state.resolved[i];
  if (!r || r === "pending" || r.kind === "unresolved") return;
  const t = resolveCue(cue, cueFrame(r));
  if (!t) return;
  if (state.index !== i) goToStop(i);
  set({ cursor: { file: r.file, line: t.line, end: t.end } });
}

// Play the tour through from the current stop (or the first), narrating
// each and moving on when the narration ends.
export function playTour() {
  const tour = activeTour();
  if (!tour) return;
  set({ playing: true });
  goToStop(state.index < 0 ? 0 : state.index);
}

export function stopTour() {
  if (state.playing) set({ playing: false });
  stopSpeaking();
  if (state.cursor) set({ cursor: null });
}

// The keyboard's play/pause: one key, whichever way the tour is going.
export function togglePlayTour() {
  if (state.playing) stopTour();
  else playTour();
}

// The keyboard's "read this one again": the current stop, or nothing when
// the tour hasn't been entered yet. Like the ▶ button, pressing it while
// the stop is being read stops the reading.
export function speakCurrentStop() {
  if (state.index >= 0) speakStop(state.index);
}

export function nextStop() {
  const tour = activeTour();
  if (!tour) return;
  goToStop(Math.min(state.index + 1, tour.stops.length - 1));
}

export function prevStop() {
  const tour = activeTour();
  if (!tour) return;
  // From "not started", back is the last stop: the tour is a ring you can
  // enter from either end.
  goToStop(
    state.index < 0 ? tour.stops.length - 1 : Math.max(state.index - 1, 0),
  );
}

// ---- driving the view ----

// Mounted once (in App). Opens the current stop whenever one is asked for:
// a symbol stop replaces the view with the symbol and its pre-expanded
// tree; a file stop opens the whole-file frame and asks it to scroll to
// the line. The resolution may still be in flight when the reader presses
// next, so this waits on it rather than opening nothing.
export function useTourDriver() {
  const store = useViewStore();
  const { openSeq, index, active } = useTours();
  useEffect(() => {
    if (openSeq === 0 || index < 0) return;
    const tour = activeTour();
    const stop = tour?.stops[index];
    if (!tour || !stop) return;
    let alive = true;
    ensureResolved(index, stop).then((r) => {
      if (!alive) return;
      if (r.kind === "symbol") {
        store.setView(r.targetId, r.tree);
        set({ focus: { file: r.file, line: r.line, seq: openSeq } });
      } else if (r.kind === "file") {
        store.setSymbol(`file:${r.file}`);
        set({ focus: { file: r.file, line: r.line, seq: openSeq } });
      }
    });
    return () => {
      alive = false;
    };
    // `active` is a dependency so a stop opened right after switching tours
    // reads the new tour, not a stale closure over the old one.
  }, [openSeq, index, active, store]);
}

// ---- what a frame renders ----

export interface TourStopCardInfo {
  index: number;
  total: number;
  tourTitle: string;
  file: string;
  line: number; // 1-based file line the card renders after
  stop: TourStop;
  current: boolean;
  missing: string[];
}

// The active tour's stops as cards anchored in file space, for frames to
// render at the right line — every stop, not only the current one, so the
// markers are there as the reader moves around on their own.
export function useTourStopCards(): TourStopCardInfo[] {
  const s = useTours();
  // Memoised on the state snapshot so a frame's per-line grouping of these
  // only recomputes when the tour actually changed.
  return useMemo(() => {
    const tour = activeTour(s);
    if (!tour) return [];
    const out: TourStopCardInfo[] = [];
    tour.stops.forEach((stop, i) => {
      const r = s.resolved[i];
      if (!r || r === "pending" || r.kind === "unresolved") return;
      out.push({
        index: i,
        total: tour.stops.length,
        tourTitle: tour.title || tour.name,
        file: r.file,
        line: r.line,
        stop,
        current: i === s.index,
        missing: r.kind === "symbol" ? r.missing : [],
      });
    });
    return out;
  }, [s]);
}
