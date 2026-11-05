import { useSyncExternalStore } from "react";

// Reader preferences. Global (one localStorage key, not per-project) —
// unlike bookmarks, these describe how *you* like to read, not anything
// about the loaded repo.
export interface Settings {
  // Colored lane per nesting level on each frame's left edge. Lanes stack
  // naturally: each frame's rail sits just right of its parent's.
  depthRails: boolean;
  // Numeric nesting depth in each frame's header.
  depthRuler: boolean;
  // How nesting is conveyed horizontally: "rails" keeps every level at
  // full reading width (the rails carry the cue); "indent" restores the
  // classic per-level indent.
  indentMode: "rails" | "indent";
  // Read each tour stop aloud as it opens (the browser's speech synthesis,
  // or a configured voice). Off by default: a voice that starts unasked is
  // a surprise.
  tourReadAloud: boolean;
  // Sonify: a quiet tone for each unfold, fold and tour step, a grain per
  // cursor move while a stop is narrated, and the `s` scan of the frame's
  // shape. Off by default for the same reason the voice is.
  sound: boolean;
  // Master volume, 0 to 1. Full is still quiet by design.
  soundVolume: number;
}

const DEFAULTS: Settings = {
  depthRails: true,
  depthRuler: false,
  indentMode: "rails",
  tourReadAloud: false,
  sound: false,
  soundVolume: 0.5,
};

const KEY = "unfold.settings";
const listeners = new Set<() => void>();

function read(): Settings {
  if (typeof localStorage === "undefined") return DEFAULTS;
  try {
    const raw = localStorage.getItem(KEY);
    const parsed = raw ? JSON.parse(raw) : {};
    return { ...DEFAULTS, ...(parsed && typeof parsed === "object" ? parsed : {}) };
  } catch {
    return DEFAULTS;
  }
}

let cache: Settings = read();

// The current settings for code that runs outside React (stores, drivers).
export function currentSettings(): Settings {
  return cache;
}

export function updateSettings(patch: Partial<Settings>) {
  cache = { ...cache, ...patch };
  try {
    localStorage.setItem(KEY, JSON.stringify(cache));
  } catch {
    /* storage unavailable — keep the in-memory copy */
  }
  for (const fn of listeners) fn();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

// Same useSyncExternalStore pattern as bookmarks.tsx: `cache` is a stable
// reference that only changes on update, so the snapshot memoizes correctly.
export function useSettings(): Settings {
  return useSyncExternalStore(
    subscribe,
    () => cache,
    () => cache,
  );
}
