import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { CallID } from "./types";

// A FrameSlice describes the *intent* for one frame in the view tree:
// which lines are folded, and which call sites are currently expanded
// (each with its impl choice). The actual loaded child frames are not
// stored here — components fetch them on demand based on this intent.
export interface FrameSlice {
  folds: [number, number][]; // [start, end] inclusive line indices
  expansions: Record<CallID, FrameSlice & { choice: number }>;
}

export type FramePath = { callId: CallID; choice: number }[];

export const emptySlice: FrameSlice = Object.freeze({
  folds: [],
  expansions: {},
});

interface ViewStoreCtx {
  getSlice: (path: FramePath) => FrameSlice;
  setFolds: (path: FramePath, folds: [number, number][]) => void;
  expand: (path: FramePath, callId: CallID, choice: number) => void;
  setChoice: (path: FramePath, callId: CallID, choice: number) => void;
  collapse: (path: FramePath, callId: CallID) => void;
  // Subscribe so consumers re-render when their slice changes.
  subscribe: (listener: () => void) => () => void;
  // Currently-selected root symbol (also tracked in the URL hash).
  symbol: string | null;
  setSymbol: (s: string | null) => void;
}

const ViewStoreContext = createContext<ViewStoreCtx | null>(null);

export function ViewStoreProvider({ children }: { children: ReactNode }) {
  // Read initial state from the URL hash. The hash carries:
  //   #symbol=<name>&v=<base64-json-of-slice-tree>
  const initial = useMemo(() => readHash(), []);
  const rootRef = useRef<FrameSlice>(initial.tree);
  const [symbol, setSymbolState] = useState<string | null>(initial.symbol);
  const listeners = useRef<Set<() => void>>(new Set());

  const notify = useCallback(() => {
    for (const fn of listeners.current) fn();
  }, []);

  const setSymbol = useCallback((s: string | null) => {
    setSymbolState(s);
  }, []);

  const getSlice = useCallback((path: FramePath): FrameSlice => {
    let cur: FrameSlice = rootRef.current;
    for (const step of path) {
      const next = cur.expansions[step.callId];
      if (!next) return emptySlice;
      cur = next;
    }
    return cur;
  }, []);

  const updatePath = useCallback(
    (path: FramePath, updater: (slice: FrameSlice) => FrameSlice) => {
      rootRef.current = mutate(rootRef.current, path, updater);
      notify();
      writeHash(symbol, rootRef.current);
    },
    [notify, symbol],
  );

  const setFolds = useCallback(
    (path: FramePath, folds: [number, number][]) => {
      updatePath(path, (s) => ({ ...s, folds }));
    },
    [updatePath],
  );

  const expand = useCallback(
    (path: FramePath, callId: CallID, choice: number) => {
      updatePath(path, (s) => ({
        ...s,
        expansions: {
          ...s.expansions,
          [callId]: { folds: [], expansions: {}, choice },
        },
      }));
    },
    [updatePath],
  );

  const setChoice = useCallback(
    (path: FramePath, callId: CallID, choice: number) => {
      updatePath(path, (s) => {
        const existing = s.expansions[callId];
        if (!existing) return s;
        // Reset the child's nested state — the new impl has different
        // call sites, so fold/expansion indices don't carry over.
        return {
          ...s,
          expansions: {
            ...s.expansions,
            [callId]: { folds: [], expansions: {}, choice },
          },
        };
      });
    },
    [updatePath],
  );

  const collapse = useCallback(
    (path: FramePath, callId: CallID) => {
      updatePath(path, (s) => {
        const next = { ...s.expansions };
        delete next[callId];
        return { ...s, expansions: next };
      });
    },
    [updatePath],
  );

  const subscribe = useCallback((listener: () => void) => {
    listeners.current.add(listener);
    return () => {
      listeners.current.delete(listener);
    };
  }, []);

  // Sync symbol changes to the URL hash too.
  useEffect(() => {
    writeHash(symbol, rootRef.current);
  }, [symbol]);

  // React to back/forward — reload state if the hash changed externally.
  useEffect(() => {
    function onHashChange() {
      const next = readHash();
      rootRef.current = next.tree;
      setSymbolState(next.symbol);
      notify();
    }
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [notify]);

  const ctx = useMemo<ViewStoreCtx>(
    () => ({ getSlice, setFolds, expand, setChoice, collapse, subscribe, symbol, setSymbol }),
    [getSlice, setFolds, expand, setChoice, collapse, subscribe, symbol, setSymbol],
  );

  return <ViewStoreContext.Provider value={ctx}>{children}</ViewStoreContext.Provider>;
}

export function useViewStore(): ViewStoreCtx {
  const ctx = useContext(ViewStoreContext);
  if (!ctx) throw new Error("useViewStore used outside ViewStoreProvider");
  return ctx;
}

// Subscribes a component to changes in the slice at `path` and returns
// the current slice. The slice reference is stable for unchanged paths.
export function useFrameSlice(path: FramePath): FrameSlice {
  const store = useViewStore();
  const [, setTick] = useState(0);
  useEffect(() => store.subscribe(() => setTick((n) => n + 1)), [store]);
  return store.getSlice(path);
}

function mutate(
  root: FrameSlice,
  path: FramePath,
  updater: (slice: FrameSlice) => FrameSlice,
): FrameSlice {
  if (path.length === 0) return updater(root);
  const [head, ...rest] = path;
  const child = root.expansions[head.callId];
  if (!child) return root;
  const newChild = mutate(child, rest, updater);
  if (newChild === child) return root;
  return {
    ...root,
    expansions: { ...root.expansions, [head.callId]: { ...newChild, choice: child.choice } },
  };
}

// ----- URL hash encode/decode -----

function readHash(): { symbol: string | null; tree: FrameSlice } {
  if (typeof location === "undefined") return { symbol: null, tree: emptySlice };
  const params = new URLSearchParams(location.hash.slice(1));
  const symbol = params.get("symbol");
  const v = params.get("v");
  let tree: FrameSlice = emptySlice;
  if (v) {
    try {
      tree = JSON.parse(decodeURIComponent(escape(atob(v))));
    } catch {
      // ignore — invalid encoding, start fresh
    }
  }
  return { symbol, tree };
}

function writeHash(symbol: string | null, tree: FrameSlice): void {
  if (typeof location === "undefined") return;
  const params = new URLSearchParams();
  if (symbol) params.set("symbol", symbol);
  if (hasState(tree)) {
    const json = JSON.stringify(tree);
    // base64 keeps it URL-safe and lets us avoid escaping JSON punctuation.
    params.set("v", btoa(unescape(encodeURIComponent(json))));
  }
  const hash = "#" + params.toString();
  if (hash === location.hash) return;
  // history.replaceState avoids polluting the back stack on every click.
  history.replaceState(null, "", location.pathname + location.search + hash);
}

function hasState(slice: FrameSlice): boolean {
  return slice.folds.length > 0 || Object.keys(slice.expansions).length > 0;
}
