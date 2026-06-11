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
  // Fan-out calls: a record exists per call whose receiver list is open;
  // each expanded receiver index maps to its own nested slice. Many can be
  // open at once (unlike `expansions`, one child per call).
  fanouts?: Record<CallID, Record<number, FrameSlice>>;
}

export type FramePath = { callId: CallID; choice: number }[];

// Stable string key for a frame path. Used to tag rendered inline frames
// (data-frame-key) so the call tree can scroll the matching frame into
// view. The empty path (root frame) maps to "".
export function pathKey(path: FramePath): string {
  return path.map((p) => `${p.callId}#${p.choice}`).join(">");
}

export function isFanoutOpen(slice: FrameSlice, callId: CallID): boolean {
  return !!slice.fanouts?.[callId];
}

export function expandedReceivers(slice: FrameSlice, callId: CallID): number[] {
  return Object.keys(slice.fanouts?.[callId] ?? {}).map(Number);
}

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
  // Bulk controls: expand several calls in one update ("+1 level"), and
  // collapse a frame's whole subtree (expansions + fanouts; folds stay).
  expandMany: (path: FramePath, callIds: CallID[]) => void;
  clearChildren: (path: FramePath) => void;
  // Fan-out calls: open/close the receiver list, and expand/collapse each
  // receiver (many can be open at once).
  openFanout: (path: FramePath, callId: CallID) => void;
  closeFanout: (path: FramePath, callId: CallID) => void;
  expandReceiver: (path: FramePath, callId: CallID, index: number) => void;
  collapseReceiver: (path: FramePath, callId: CallID, index: number) => void;
  // Subscribe so consumers re-render when their slice changes.
  subscribe: (listener: () => void) => () => void;
  // Currently-selected root symbol (also tracked in the URL hash).
  symbol: string | null;
  setSymbol: (s: string | null) => void;
  // Replace the whole view atomically: new root symbol AND a prebuilt slice
  // tree. Used to re-root onto a caller (the old view nests inside the new
  // root at the caller's call site) and to load a pre-unfolded caller chain.
  setView: (symbol: string | null, tree: FrameSlice) => void;
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

  const setView = useCallback(
    (s: string | null, tree: FrameSlice) => {
      rootRef.current = tree;
      setSymbolState(s);
      notify();
      writeHash(s, tree);
    },
    [notify],
  );

  const getSlice = useCallback((path: FramePath): FrameSlice => {
    let cur: FrameSlice = rootRef.current;
    for (const step of path) {
      const fanout = cur.fanouts?.[step.callId]?.[step.choice];
      const expansion = cur.expansions[step.callId];
      const next =
        fanout ?? (expansion && expansion.choice === step.choice ? expansion : undefined);
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

  const clearChildren = useCallback(
    (path: FramePath) => {
      updatePath(path, (s) => {
        if (Object.keys(s.expansions).length === 0 && !s.fanouts) return s;
        // Keep folds — they're this frame's own state, not its subtree.
        return { folds: s.folds, expansions: {} };
      });
    },
    [updatePath],
  );

  const expandMany = useCallback(
    (path: FramePath, callIds: CallID[]) => {
      if (callIds.length === 0) return;
      updatePath(path, (s) => {
        const expansions = { ...s.expansions };
        for (const id of callIds) {
          expansions[id] = { folds: [], expansions: {}, choice: 0 };
        }
        return { ...s, expansions };
      });
    },
    [updatePath],
  );

  const openFanout = useCallback(
    (path: FramePath, callId: CallID) => {
      updatePath(path, (s) =>
        s.fanouts?.[callId] ? s : { ...s, fanouts: { ...s.fanouts, [callId]: {} } },
      );
    },
    [updatePath],
  );

  const closeFanout = useCallback(
    (path: FramePath, callId: CallID) => {
      updatePath(path, (s) => {
        if (!s.fanouts?.[callId]) return s;
        const next = { ...s.fanouts };
        delete next[callId];
        return { ...s, fanouts: next };
      });
    },
    [updatePath],
  );

  const expandReceiver = useCallback(
    (path: FramePath, callId: CallID, index: number) => {
      updatePath(path, (s) => ({
        ...s,
        fanouts: {
          ...s.fanouts,
          [callId]: { ...(s.fanouts?.[callId] ?? {}), [index]: emptySlice },
        },
      }));
    },
    [updatePath],
  );

  const collapseReceiver = useCallback(
    (path: FramePath, callId: CallID, index: number) => {
      updatePath(path, (s) => {
        const cur = s.fanouts?.[callId];
        if (!cur || !(index in cur)) return s;
        const next = { ...cur };
        delete next[index];
        return { ...s, fanouts: { ...s.fanouts, [callId]: next } };
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
    () => ({
      getSlice,
      setFolds,
      expand,
      setChoice,
      collapse,
      expandMany,
      clearChildren,
      openFanout,
      closeFanout,
      expandReceiver,
      collapseReceiver,
      subscribe,
      symbol,
      setSymbol,
      setView,
    }),
    [
      getSlice,
      setFolds,
      expand,
      setChoice,
      collapse,
      expandMany,
      clearChildren,
      openFanout,
      closeFanout,
      expandReceiver,
      collapseReceiver,
      subscribe,
      symbol,
      setSymbol,
      setView,
    ],
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

  // Fan-out receiver child?
  const fanoutChild = root.fanouts?.[head.callId]?.[head.choice];
  if (fanoutChild) {
    const newChild = mutate(fanoutChild, rest, updater);
    if (newChild === fanoutChild) return root;
    return {
      ...root,
      fanouts: {
        ...root.fanouts,
        [head.callId]: { ...root.fanouts![head.callId], [head.choice]: newChild },
      },
    };
  }

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
  return (
    slice.folds.length > 0 ||
    Object.keys(slice.expansions).length > 0 ||
    Object.keys(slice.fanouts ?? {}).length > 0
  );
}
