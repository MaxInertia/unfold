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
import { LEVELS, type ZoomLevel } from "./zoom";

// A FrameSlice describes the *intent* for one frame in the view tree:
// which lines are folded, and which call sites are currently expanded
// (each with its impl choice). The actual loaded child frames are not
// stored here — components fetch them on demand based on this intent.
export interface FrameSlice {
  folds: [number, number][]; // [start, end] inclusive line indices
  expansions: Record<CallID, FrameSlice & { choice: number }>;
  // Elided: this frame's own source is hidden, but what it expands into is
  // not — the children render in its place. It is deliberately *not* the same
  // as collapsing, which would take the children with it. Eliding is how you
  // bring two distant frames next to each other: with A → B → C all open,
  // eliding B puts C directly under A's call site.
  //
  // It lives here rather than in component state because it's part of the
  // view — a shared link should reproduce what the sender was looking at.
  elided?: boolean;
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
  // Hide a frame's own source while keeping what it expanded into. See
  // FrameSlice.elided.
  setElided: (path: FramePath, elided: boolean) => void;
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
  // Which rung of the zoom ladder is on screen, and — above the frame — which
  // service the upper levels are about. Both live here rather than in App so
  // the hash has exactly one writer: two of them race, and the loser silently
  // drops whichever half of the state it didn't know about.
  zoom: ZoomLevel;
  setZoom: (z: ZoomLevel) => void;
  service: string | null;
  setService: (alias: string | null) => void;
  // Picking a service *and* descending to it is one navigation, so it commits
  // once. Two calls would cost two history entries for a single click.
  openService: (alias: string) => void;
}

const ViewStoreContext = createContext<ViewStoreCtx | null>(null);

export function ViewStoreProvider({ children }: { children: ReactNode }) {
  // Read initial state from the URL hash. The hash carries:
  //   #symbol=<name>&zoom=<level>&svc=<alias>&v=<base64-json-of-slice-tree>
  const initial = useMemo(() => readHash(), []);
  // One ref holds everything the URL encodes. Callbacks write through it
  // rather than closing over individual pieces of state — a stale capture
  // here doesn't misrender, it writes a hash missing whatever the closure
  // was too old to see, which then loads back wrong.
  const urlRef = useRef<UrlState>(initial);
  const rootRef = useRef<FrameSlice>(initial.tree);
  const [symbol, setSymbolState] = useState<string | null>(initial.symbol);
  const [zoom, setZoomState] = useState<ZoomLevel>(initial.zoom);
  const [service, setServiceState] = useState<string | null>(initial.service);
  const listeners = useRef<Set<() => void>>(new Set());

  const notify = useCallback(() => {
    for (const fn of listeners.current) fn();
  }, []);

  // Commit a change to the URL-backed state. `mode` is the whole point:
  // expansions replace the current entry (a hundred clicks shouldn't cost a
  // hundred presses of back), navigations push a new one.
  const commit = useCallback(
    (patch: Partial<UrlState>, mode: HistoryMode) => {
      const next = { ...urlRef.current, ...patch };
      urlRef.current = next;
      rootRef.current = next.tree;
      setSymbolState(next.symbol);
      setZoomState(next.zoom);
      setServiceState(next.service);
      notify();
      writeHash(next, mode);
    },
    [notify],
  );

  // Opening a symbol is one navigation, so it lands in the code and drops any
  // explicit service pick in the *same* history entry — the frame now decides
  // which service you're in. Doing this as a separate effect (as App used to)
  // both split it across two entries and fired on history restores, undoing
  // the zoom level the URL had just asked for.
  const setSymbol = useCallback(
    (s: string | null) => {
      commit({ symbol: s, zoom: "frame", service: null }, "push");
    },
    [commit],
  );

  const setZoom = useCallback(
    (z: ZoomLevel) => {
      if (urlRef.current.zoom === z) return;
      commit({ zoom: z }, "push");
    },
    [commit],
  );

  const setService = useCallback(
    (alias: string | null) => {
      if (urlRef.current.service === alias) return;
      commit({ service: alias }, "push");
    },
    [commit],
  );

  const openService = useCallback(
    (alias: string) => {
      commit({ service: alias, zoom: "service" }, "push");
    },
    [commit],
  );

  const setView = useCallback(
    (s: string | null, tree: FrameSlice) => {
      // Re-rooting is a lateral move, not a zoom: it replaces the view, so it
      // earns a history entry the same way opening a symbol does.
      commit({ symbol: s, tree, zoom: "frame", service: null }, "push");
    },
    [commit],
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
      const tree = mutate(rootRef.current, path, updater);
      urlRef.current = { ...urlRef.current, tree };
      rootRef.current = tree;
      notify();
      // Expanding is reading, not navigating — it replaces rather than pushes,
      // or back becomes a per-click undo of every fold you ever opened.
      writeHash(urlRef.current, "replace");
    },
    [notify],
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
        return dropDeadElision({ ...s, expansions: next });
      });
    },
    [updatePath],
  );

  const clearChildren = useCallback(
    (path: FramePath) => {
      updatePath(path, (s) => {
        if (Object.keys(s.expansions).length === 0 && !s.fanouts) return s;
        // Keep folds — they're this frame's own state, not its subtree.
        // The elision goes, because it only ever described a subtree.
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

  const setElided = useCallback(
    (path: FramePath, elided: boolean) => {
      updatePath(path, (s) => {
        if (!!s.elided === elided) return s;
        if (!elided) {
          const { elided: _drop, ...rest } = s;
          return rest;
        }
        return { ...s, elided: true };
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
        return dropDeadElision({ ...s, fanouts: { ...s.fanouts, [callId]: next } });
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

  // React to back/forward. popstate covers history moves; hashchange covers a
  // hash the user edited by hand. Both re-read rather than trying to invert
  // the transition, so restoring is the same code path as loading a shared
  // link — there's only one way to get from a hash to a view.
  useEffect(() => {
    function restore() {
      const next = readHash();
      urlRef.current = next;
      rootRef.current = next.tree;
      setSymbolState(next.symbol);
      setZoomState(next.zoom);
      setServiceState(next.service);
      notify();
    }
    window.addEventListener("popstate", restore);
    window.addEventListener("hashchange", restore);
    return () => {
      window.removeEventListener("popstate", restore);
      window.removeEventListener("hashchange", restore);
    };
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
      setElided,
      openFanout,
      closeFanout,
      expandReceiver,
      collapseReceiver,
      subscribe,
      symbol,
      setSymbol,
      setView,
      zoom,
      setZoom,
      service,
      setService,
      openService,
    }),
    [
      getSlice,
      setFolds,
      expand,
      setChoice,
      collapse,
      expandMany,
      clearChildren,
      setElided,
      openFanout,
      closeFanout,
      expandReceiver,
      collapseReceiver,
      subscribe,
      symbol,
      setSymbol,
      setView,
      zoom,
      setZoom,
      service,
      setService,
      openService,
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

// An elision with nothing left under it describes nothing: the frame renders
// its body again, and a stale flag would silently re-hide it the moment
// something was expanded there next. Closing the last child clears it.
function dropDeadElision(s: FrameSlice): FrameSlice {
  if (!s.elided) return s;
  const children =
    Object.keys(s.expansions).length +
    Object.values(s.fanouts ?? {}).reduce((n, r) => n + Object.keys(r).length, 0);
  if (children > 0) return s;
  const { elided: _drop, ...rest } = s;
  return rest;
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

// Everything the URL round-trips. A history entry is a whole view-state, not
// just a position: coming forward to a lateral move has to restore the trace
// that was built there, not only where the cursor was.
export interface UrlState {
  symbol: string | null;
  zoom: ZoomLevel;
  service: string | null;
  tree: FrameSlice;
}

type HistoryMode = "push" | "replace";

function readHash(): UrlState {
  if (typeof location === "undefined") {
    return { symbol: null, zoom: "frame", service: null, tree: emptySlice };
  }
  const params = new URLSearchParams(location.hash.slice(1));
  const symbol = params.get("symbol");
  const service = params.get("svc");
  // An unknown level would strand the UI on a rung nothing renders, so a
  // hand-edited or stale value falls back rather than being trusted.
  const raw = params.get("zoom");
  const zoom = LEVELS.includes(raw as ZoomLevel) ? (raw as ZoomLevel) : "frame";
  const v = params.get("v");
  let tree: FrameSlice = emptySlice;
  if (v) {
    try {
      tree = JSON.parse(decodeURIComponent(escape(atob(v))));
    } catch {
      // ignore — invalid encoding, start fresh
    }
  }
  return { symbol, zoom, service, tree };
}

function writeHash(state: UrlState, mode: HistoryMode): void {
  if (typeof location === "undefined") return;
  const params = new URLSearchParams();
  if (state.symbol) params.set("symbol", state.symbol);
  // "frame" is the default, so leaving it out keeps the common link short.
  if (state.zoom !== "frame") params.set("zoom", state.zoom);
  if (state.service) params.set("svc", state.service);
  if (hasState(state.tree)) {
    const json = JSON.stringify(state.tree);
    // base64 keeps it URL-safe and lets us avoid escaping JSON punctuation.
    params.set("v", btoa(unescape(encodeURIComponent(json))));
  }
  const hash = "#" + params.toString();
  if (hash === location.hash) return;
  const url = location.pathname + location.search + hash;
  // A push that lands on the state we're already showing is a duplicate the
  // user has to press back through twice, so identical hashes are dropped
  // above regardless of mode.
  if (mode === "push") history.pushState(null, "", url);
  else history.replaceState(null, "", url);
}

function hasState(slice: FrameSlice): boolean {
  return (
    slice.folds.length > 0 ||
    !!slice.elided ||
    Object.keys(slice.expansions).length > 0 ||
    Object.keys(slice.fanouts ?? {}).length > 0
  );
}
