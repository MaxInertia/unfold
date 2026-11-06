import { useEffect, useMemo, useState } from "react";
import { peekCallFrame, subscribeFrameCache } from "./frameCache";
import { activeTour, useTours } from "./tours";
import type { CallID, Frame } from "./types";
import { useFrameSlice, type FrameSlice } from "./viewState";

// What the city marks, gathered from the stores that already know it: which
// frames are unfolded under the root, and where the active tour's stops are.
// Kept out of City.tsx so the component is about drawing, and out of App so
// the shell doesn't re-render every time a child frame arrives.

export type PeekFrame = (callId: CallID, choice: number) => Frame | undefined;

// Target ids of every frame unfolded under `root`, walking the slice tree.
// The store knows an expansion by call id and choice; the target it opened is
// the child frame's own id, which is in the frame cache once it has loaded.
// Until then the call site can usually say — a direct call names its target,
// an interface call its candidates, a fan-out its receivers — so a building
// lights the moment it is expanded rather than a round trip later.
export function unfoldedTargets(
  root: Frame | null,
  slice: FrameSlice,
  peek: PeekFrame,
): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (id: string | undefined) => {
    if (id && !seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  };
  const walk = (frame: Frame, s: FrameSlice) => {
    for (const [callId, child] of Object.entries(s.expansions)) {
      const site = frame.calls.find((c) => c.id === callId);
      const loaded = peek(callId, child.choice);
      add(
        loaded?.id ??
          site?.candidates?.[child.choice]?.targetId ??
          site?.targetId,
      );
      if (loaded) walk(loaded, child);
    }
    for (const [callId, receivers] of Object.entries(s.fanouts ?? {})) {
      const site = frame.calls.find((c) => c.id === callId);
      for (const [index, child] of Object.entries(receivers)) {
        const i = Number(index);
        const loaded = peek(callId, i);
        add(loaded?.id ?? site?.receivers?.[i]?.targetId);
        if (loaded) walk(loaded, child);
      }
    }
  };
  if (root) walk(root, slice);
  return out;
}

export function useUnfoldedTargets(root: Frame | null): string[] {
  const slice = useFrameSlice([]);
  // A frame arriving in the cache changes the answer without changing the
  // slice, so the walk is redone on either.
  const [tick, setTick] = useState(0);
  useEffect(() => subscribeFrameCache(() => setTick((n) => n + 1)), []);
  return useMemo(
    () => unfoldedTargets(root, slice, peekCallFrame),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- tick is the cache's change signal
    [root, slice, tick],
  );
}

// Target id → the 1-based stop numbers of the active tour that resolved to it.
// A stop given as a file and line has no function to stand on, so it isn't on
// the map; the tour panel still lists it.
export function tourStopTargets(
  stops: { length: number },
  resolved: Record<number, { kind: string; targetId?: string } | "pending">,
): Map<string, number[]> {
  const out = new Map<string, number[]>();
  for (let i = 0; i < stops.length; i++) {
    const r = resolved[i];
    if (!r || r === "pending" || r.kind !== "symbol" || !r.targetId) continue;
    const list = out.get(r.targetId) ?? [];
    list.push(i + 1);
    out.set(r.targetId, list);
  }
  return out;
}

export function useTourStopTargets(): Map<string, number[]> {
  const s = useTours();
  return useMemo(() => {
    const tour = activeTour(s);
    return tour
      ? tourStopTargets(tour.stops, s.resolved)
      : new Map<string, number[]>();
  }, [s]);
}

// Whether the viewport has room for the city beside the code. Below the
// breakpoint it floats over the corner instead.
export function useWideViewport(minWidth = 1500): boolean {
  const query = `(min-width: ${minWidth}px)`;
  const [wide, setWide] = useState(
    () => typeof matchMedia !== "undefined" && matchMedia(query).matches,
  );
  useEffect(() => {
    if (typeof matchMedia === "undefined") return;
    const mq = matchMedia(query);
    const on = () => setWide(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [query]);
  return wide;
}
