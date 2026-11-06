import { useSyncExternalStore } from "react";
import { fetchDiffTour } from "./api";
import { setGeneratedTour } from "./tours";
import type { DiffFunction, DiffTour, TargetID, Tour } from "./types";

// The diff tour: what this branch changes vs the diff base, in execution
// order, with the entrypoints that reach each change. One fetch feeds two
// things — the generated "Changes vs <base>" tour in the tour tab, and the
// blast-radius count on a changed frame's header — so they can't disagree.
//
// Same subscribe shape as bookmarks/notes/tours.

export interface DiffTourState {
  // Whether diff mode is on at all, from /api/health. Off means there is
  // nothing to fetch and nothing to show, not that the fetch failed.
  enabled: boolean;
  loaded: boolean;
  data: DiffTour | null;
}

let state: DiffTourState = { enabled: false, loaded: false, data: null };
const listeners = new Set<() => void>();
let generation = 0;

function set(patch: Partial<DiffTourState>) {
  state = { ...state, ...patch };
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useDiffTour(): DiffTourState {
  return useSyncExternalStore(
    subscribe,
    () => state,
    () => state,
  );
}

// Called with the health response's diff flag, on boot and after every
// reindex: the set of changes moves with the index.
export function setDiffMode(on: boolean) {
  if (!on) {
    generation++;
    set({ enabled: false, loaded: true, data: null });
    setGeneratedTour(null);
    return;
  }
  set({ enabled: true });
  void loadDiffTour();
}

export function loadDiffTour(): Promise<void> {
  const gen = ++generation;
  return fetchDiffTour()
    .then((data) => {
      if (gen !== generation) return;
      set({ loaded: true, data });
      setGeneratedTour(asTour(data));
    })
    .catch(() => {
      if (gen !== generation) return;
      set({ loaded: true, data: null });
      setGeneratedTour(null);
    });
}

// The server's tour, stamped so the tour store can tell a recomputation
// from a refetch of the same thing: the tours list re-resolves its stops
// when updatedAt moves, and a file's mtime is what moves it for disk tours.
export function asTour(d: DiffTour): Tour {
  return { ...d.tour, generated: true, updatedAt: signature(d) };
}

export function signature(d: DiffTour): string {
  return `${d.commit ?? ""}#${d.functions.map((f) => `${f.id}:${f.added}:${f.removed}`).join(",")}`;
}

// The changed function record for a frame, or null when the frame isn't one
// the branch changed (or diff mode is off).
export function blastFor(data: DiffTour | null, id: TargetID): DiffFunction | null {
  if (!data) return null;
  return data.functions.find((f) => f.id === id) ?? null;
}

// A route's path as a tour `expand` entry from the entrypoint: the hops
// after it, named as the server names them, joined the way an author would
// write them. Empty when the entrypoint is the function itself.
export function pathExpand(path: TargetID[], names: Record<TargetID, string>): string {
  return path
    .slice(1)
    .map((id) => names[id] ?? id)
    .join(" > ");
}

// How an entrypoint is labelled in a list: what kind of way in it is, and
// the route or RPC when it has one.
export function entryLabel(e: { kind: string; key?: string }): string {
  if (e.key && e.kind !== "init") return `${e.kind} ${e.key}`;
  return e.kind;
}
