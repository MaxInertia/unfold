import { useEffect, useState } from "react";
import { fetchBodyByCall } from "./api";
import type { CallID, Frame } from "./types";

// A small module-level cache for frames fetched by call site. The call
// tree expands many nodes and the same callee may appear under several
// parents, so caching by (callId, choice) avoids refetching. It also
// dedupes concurrent fetches for the same key.
const cache = new Map<string, Frame>();
const inflight = new Map<string, Promise<Frame>>();

function keyFor(callId: CallID, choice: number): string {
  return `${callId}:${choice}`;
}

// The city map lights every frame that is unfolded under the root, and the
// view store only knows those frames by call id — the target they resolved
// to is in the frame itself, which is here. So the cache can be read without
// fetching, and watched: a frame arriving is what turns a call id into a lit
// building.
const watchers = new Set<() => void>();

export function peekCallFrame(callId: CallID, choice: number): Frame | undefined {
  return cache.get(keyFor(callId, choice));
}

export function subscribeFrameCache(fn: () => void): () => void {
  watchers.add(fn);
  return () => {
    watchers.delete(fn);
  };
}

function remember(key: string, frame: Frame) {
  cache.set(key, frame);
  for (const fn of watchers) fn();
}

// Frame.tsx fetches its own children and receivers rather than going through
// useCallFrame (it tags them with the choice and revision they were fetched
// under), so it reports what it got here, or the city would only know about
// frames the call tree happened to open.
export function rememberCallFrame(callId: CallID, choice: number, frame: Frame): void {
  remember(keyFor(callId, choice), frame);
}

interface FrameState {
  frame: Frame | null;
  loading: boolean;
  error: string | null;
}

// useCallFrame fetches (and caches) the frame for a call's chosen target.
// Pass callId=null to mean "don't fetch" (e.g. a collapsed node).
export function useCallFrame(callId: CallID | null, choice: number): FrameState {
  const key = callId ? keyFor(callId, choice) : null;
  const [state, setState] = useState<FrameState>(() =>
    key && cache.has(key)
      ? { frame: cache.get(key)!, loading: false, error: null }
      : { frame: null, loading: key !== null, error: null },
  );

  useEffect(() => {
    if (!callId || !key) {
      setState({ frame: null, loading: false, error: null });
      return;
    }
    const cached = cache.get(key);
    if (cached) {
      setState({ frame: cached, loading: false, error: null });
      return;
    }

    let alive = true;
    setState({ frame: null, loading: true, error: null });

    let p = inflight.get(key);
    if (!p) {
      p = fetchBodyByCall(callId, choice);
      inflight.set(key, p);
      p.then((f) => remember(key, f)).finally(() => inflight.delete(key));
    }
    p.then((f) => {
      if (alive) setState({ frame: f, loading: false, error: null });
    }).catch((e: Error) => {
      if (alive) setState({ frame: null, loading: false, error: e.message });
    });

    return () => {
      alive = false;
    };
  }, [callId, choice, key]);

  return state;
}
