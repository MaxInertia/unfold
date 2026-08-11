import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { Root as HastRoot } from "hast";
import { fetchBodyByCall, fetchTypeInfo, openInEditor } from "./api";
import { highlightToHast } from "./highlight";
import { renderHast, type LineAction } from "./hastRender";
import type { CallID, CallSite, Frame as FrameT, Note, NoteAnchor, TargetID, TypeInfo } from "./types";
import {
  expandedReceivers,
  isFanoutOpen,
  pathKey,
  useFrameSlice,
  useViewStore,
  type FramePath,
} from "./viewState";
import { useBookmarks } from "./bookmarks";
import { useReloadRevision } from "./reload";
import { RecognizeCall } from "./RecognizeCall";
import { LeafCard } from "./LeafCard";
import { CallersPanel } from "./Callers";
import { depthColor } from "./StickyHeaders";
import { useSettings } from "./settings";
import { matches } from "./keybindings";
import { useNotes } from "./notes";
import { openRules } from "./rules";
import { NoteCard, NoteComposer } from "./NotesUI";

export interface FoldRange {
  start: number;
  end: number;
}

interface FrameProps {
  frame: FrameT;
  path: FramePath;
  onClose?: () => void;
  // Target ids of the frames above this one in the view (root first).
  // A call site whose target appears here (or is this frame itself) is
  // recursive: expanding it would re-open a function already on screen.
  ancestors?: TargetID[];
  // Zoom out to the service level. Passed only to the root frame — the
  // header control and the trail are two gestures for the same move, kept
  // side by side so one can be picked after using both.
  onZoomOut?: () => void;
}

export function Frame({ frame, path, onClose, ancestors = [], onZoomOut }: FrameProps) {
  const store = useViewStore();
  const slice = useFrameSlice(path);
  const bookmarks = useBookmarks();
  const [hast, setHast] = useState<HastRoot | null>(null);
  // Loaded child frames are component-local; the URL only persists the
  // intent (which calls are expanded with what choice).
  const [loadedChildren, setLoadedChildren] = useState<Map<CallID, FrameT>>(new Map());
  const [loading, setLoading] = useState<Set<CallID>>(new Set());
  const [errors, setErrors] = useState<Map<CallID, string>>(new Map());
  // Fan-out receiver frames, keyed by `${callId}#${receiverIndex}` (a fan-out
  // call can have many receivers open at once, unlike a normal expansion which
  // has a single child). Errors share the same composite key.
  const [fanoutChildren, setFanoutChildren] = useState<Map<string, FrameT>>(new Map());
  const [fanoutErrors, setFanoutErrors] = useState<Map<string, string>>(new Map());
  const fanoutLoading = useRef<Set<string>>(new Set());
  const [selection, setSelection] = useState<{ anchor: number; head: number } | null>(null);
  const [callersOpen, setCallersOpen] = useState(false);
  // Which boundaries have been opened. A boundary rests as a hint at the end
  // of its line and only becomes a card when asked for — every other
  // affordance here is on demand (a type card on hover, callers on click, the
  // impl switcher once expanded), and a band of chrome between two lines of
  // code was the one that wasn't.
  const [boundariesOpen, setBoundariesOpen] = useState<Set<CallID>>(new Set());
  const settings = useSettings();
  // A rebuilt index is a reason to refetch a body, not to throw the view away.
  const revision = useReloadRevision();
  const depth = path.length;
  const [typeCard, setTypeCard] = useState<{ x: number; y: number; info: TypeInfo } | null>(null);
  const [recognizing, setRecognizing] = useState(false);
  // gen stamps hover lookups so a reply that arrives after the pointer has
  // moved on can be discarded instead of overwriting what's on screen.
  const hoverRef = useRef({ offset: -1, showTimer: 0, hideTimer: 0, gen: 0 });
  const typeCardRef = useRef<HTMLDivElement>(null);
  const allNotes = useNotes();
  const [composing, setComposing] = useState<NoteAnchor | null>(null);
  const isFileFrame = frame.id.startsWith("file:");

  const sourceLines = useMemo(() => frame.source.split("\n"), [frame.source]);

  // Notes anchored inside this frame's line range, keyed by the rendered
  // line index their card appears after. Anchors live in file space, so
  // the same note shows in a function frame and the whole-file frame.
  const notesByLine = useMemo(() => {
    const m = new Map<number, Note[]>();
    for (const n of allNotes) {
      const a = n.anchor;
      if (a.file !== frame.file) continue;
      if (a.kind !== "after-line" && a.kind !== "range") continue;
      const end = a.endLine ?? 0;
      if (end < frame.startLine || end > frame.endLine) continue;
      const idx = end - frame.startLine;
      m.set(idx, [...(m.get(idx) ?? []), n]);
    }
    return m;
  }, [allNotes, frame.file, frame.startLine, frame.endLine]);

  // Lines covered by a range anchor get a subtle tint.
  const notedLines = useMemo(() => {
    const s = new Set<number>();
    for (const n of allNotes) {
      const a = n.anchor;
      if (a.file !== frame.file || a.kind !== "range") continue;
      for (let l = a.startLine ?? 0; l <= (a.endLine ?? 0); l++) {
        const idx = l - frame.startLine;
        if (idx >= 0 && idx <= frame.endLine - frame.startLine) s.add(idx);
      }
    }
    return s;
  }, [allNotes, frame.file, frame.startLine, frame.endLine]);

  const fileStartNotes = isFileFrame
    ? allNotes.filter((n) => n.anchor.file === frame.file && n.anchor.kind === "file-start")
    : [];
  const fileEndNotes = isFileFrame
    ? allNotes.filter((n) => n.anchor.file === frame.file && n.anchor.kind === "file-end")
    : [];

  // A note is drifted when its anchored line's text no longer matches the
  // snapshot taken at save time — the file was edited underneath it.
  function isDrifted(n: Note): boolean {
    const a = n.anchor;
    if (!a.snippet || a.endLine == null) return false;
    const cur = sourceLines[a.endLine - frame.startLine];
    return cur === undefined || cur.trim() !== a.snippet.trim();
  }

  function noteSelection() {
    if (!selection) return;
    const lo = Math.min(selection.anchor, selection.head);
    const hi = Math.max(selection.anchor, selection.head);
    setComposing({
      file: frame.file,
      kind: lo === hi ? "after-line" : "range",
      startLine: frame.startLine + lo,
      endLine: frame.startLine + hi,
      snippet: sourceLines[hi] ?? "",
    });
    setSelection(null);
  }

  // UTF-16 offset of each line's start in the source, for hover→offset mapping.
  const lineStarts = useMemo(() => {
    const starts = [0];
    for (let i = 0; i < frame.source.length; i++) {
      if (frame.source.charCodeAt(i) === 10) starts.push(i + 1);
    }
    return starts;
  }, [frame.source]);

  function offsetAtPoint(clientX: number, clientY: number): number | null {
    const caret = caretFromPoint(clientX, clientY);
    if (!caret) return null;
    const startEl =
      caret.node.nodeType === Node.TEXT_NODE
        ? caret.node.parentElement
        : (caret.node as Element);
    const lineSpan = startEl?.closest(".line") as HTMLElement | null;
    const row = startEl?.closest(".line-row") as HTMLElement | null;
    if (!lineSpan || !row) return null;
    // caretFromPoint snaps to the *nearest* text position, so a pointer in
    // the blank area right of a line (or on an empty line) still yields an
    // offset and would pop the type card. Only accept the hit when the
    // pointer is horizontally inside the line's actual text.
    //
    // Measure the rendered text via a range over the line's contents, NOT
    // lineSpan.getBoundingClientRect(): `.line` is a flex child (`flex: 1 1
    // auto`) that stretches to fill the row, so its box spans the full width
    // and its right edge is useless here. A range's rect hugs the real glyphs,
    // so the empty space past the code is correctly rejected (and an empty line
    // yields a zero-width rect that rejects everything).
    const textRange = document.createRange();
    textRange.selectNodeContents(lineSpan);
    const textRect = textRange.getBoundingClientRect();
    if (clientX < textRect.left || clientX > textRect.right) return null;
    const lineIdx = Number(row.getAttribute("data-line-idx"));
    if (!Number.isFinite(lineIdx)) return null;
    const measure = document.createRange();
    measure.setStart(lineSpan, 0);
    try {
      measure.setEnd(caret.node, caret.offset);
    } catch {
      return null;
    }
    return (lineStarts[lineIdx] ?? 0) + measure.toString().length;
  }

  // Every pending hover effect goes through these three, and they always clear
  // *both* timers before arming one. Assigning a fresh id over an armed one
  // leaks it: the handle is gone, so nothing can cancel it, and it fires later
  // into a UI that has moved on. That is how the card kept vanishing under the
  // pointer — a blank-area move armed a hide, the very next event (leaving the
  // source for the card) overwrote the handle with its own, and clearing that
  // one on entry left the first still counting down.
  function cancelHover() {
    window.clearTimeout(hoverRef.current.showTimer);
    window.clearTimeout(hoverRef.current.hideTimer);
    hoverRef.current.offset = -1;
    // Also abandon a lookup already in flight: clearTimeout can't reach a
    // fetch that has left, and its reply would otherwise land on a card the
    // pointer is now inside.
    hoverRef.current.gen++;
  }

  function scheduleHide() {
    cancelHover();
    hoverRef.current.hideTimer = window.setTimeout(() => setTypeCard(null), 200);
  }

  // Dismissing the card also drops the authoring form. Without this the flag
  // outlives the card that owned it, and the *next* symbol you hover opens
  // straight into a half-filled rule for something else.
  function closeTypeCard() {
    cancelHover();
    setRecognizing(false);
    setTypeCard(null);
  }

  function onSourceMouseMove(e: React.MouseEvent) {
    // Child frames render inside this frame's `.frame-source`, so a mousemove
    // over a nested frame bubbles up here too. Left unchecked, every ancestor
    // frame would recompute a type lookup against *its own* source/id and pop a
    // card at the same point — with the outermost (first-level) card painting on
    // top, masking the correct one. Stop at the innermost frame under the cursor.
    e.stopPropagation();
    if (selection) return; // don't fight a line selection
    // Once the form is open the card is no longer a hover affordance: it's
    // something being filled in, and code passing under the pointer must not
    // replace or dismiss it.
    if (recognizing) return;
    const off = offsetAtPoint(e.clientX, e.clientY);
    if (off == null) {
      // Not over text (blank area beside/below a line): cancel any pending
      // lookup and fade the card, same as leaving the source entirely.
      scheduleHide();
      return;
    }
    if (off === hoverRef.current.offset) return;
    window.clearTimeout(hoverRef.current.showTimer);
    window.clearTimeout(hoverRef.current.hideTimer);
    hoverRef.current.offset = off;
    const x = e.clientX;
    const y = e.clientY;
    const gen = ++hoverRef.current.gen;
    hoverRef.current.showTimer = window.setTimeout(() => {
      fetchTypeInfo(frame.id, off)
        .then((info) => {
          if (hoverRef.current.gen !== gen) return;
          setTypeCard(info ? { x, y, info } : null);
        })
        .catch(() => {
          if (hoverRef.current.gen === gen) setTypeCard(null);
        });
    }, 250);
  }

  function onSourceMouseLeave() {
    if (recognizing) return;
    scheduleHide();
  }

  function openDefinition(info: TypeInfo) {
    closeTypeCard();
    if (info.targetId) {
      store.setSymbol(info.targetId);
      return;
    }
    const at = info.definedAt;
    if (!at) return;
    const i = at.lastIndexOf(":");
    if (i > 0) openInEditor(at.slice(0, i), Number(at.slice(i + 1)) || 1).catch(() => {});
  }

  // Fetch any expanded children we don't have loaded yet, and prune any
  // we've loaded but the slice no longer expands.
  useEffect(() => {
    const wantedIds = new Set(Object.keys(slice.expansions) as CallID[]);
    // Drop loaded frames whose call is no longer expanded.
    setLoadedChildren((current) => {
      let mutated = false;
      const next = new Map(current);
      for (const id of next.keys()) {
        if (!wantedIds.has(id)) {
          next.delete(id);
          mutated = true;
        }
      }
      return mutated ? next : current;
    });

    let alive = true;
    // Only this frame's own call sites can be fetched here. A slice can carry
    // entries this frame has no call for — a leaf's spliced remote body, whose
    // slot exists to give that subtree a path in the store, and a stale id
    // from a shared URL. Both used to be fetched by call id and fail.
    const own = new Set(frame.calls.map((c) => c.id));
    for (const cid of wantedIds) {
      const want = slice.expansions[cid];
      if (!want || !own.has(cid)) continue;
      const loaded = loadedChildren.get(cid) as
        | (FrameT & { __choice?: number; __rev?: number })
        | undefined;
      // Refetch when it isn't loaded, when the chosen implementation changed,
      // or when the index has been rebuilt under it. The old body stays on
      // screen until the new one arrives: a reindex used to remount the whole
      // tree, so every open frame vanished and came back seconds later, which
      // is a redraw the reader has to recover from rather than a refresh.
      if (loaded && loaded.__choice === want.choice && loaded.__rev === revision) continue;
      if (loading.has(cid)) continue;
      setLoading((s) => new Set(s).add(cid));
      fetchBodyByCall(cid, want.choice)
        .then((child) => {
          if (!alive) return;
          // Tag with the choice and the index revision it came from, so both
          // a switched implementation and a rebuilt index are detectable.
          Object.assign(child as FrameT & { __choice?: number; __rev?: number }, {
            __choice: want.choice,
            __rev: revision,
          });
          setLoading((s) => {
            const n = new Set(s);
            n.delete(cid);
            return n;
          });
          setLoadedChildren((m) => new Map(m).set(cid, child));
          setErrors((m) => {
            const n = new Map(m);
            n.delete(cid);
            return n;
          });
        })
        .catch((err: Error) => {
          if (!alive) return;
          setLoading((s) => {
            const n = new Set(s);
            n.delete(cid);
            return n;
          });
          setErrors((m) => new Map(m).set(cid, err.message));
        });
    }
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slice.expansions, revision]);

  // Fetch the body of each expanded fan-out receiver, and prune frames for
  // receivers that have since been collapsed. Each receiver resolves to its
  // own frame via FrameForCall(callId, receiverIndex).
  useEffect(() => {
    const wanted: { key: string; callId: CallID; index: number }[] = [];
    for (const callId of Object.keys(slice.fanouts ?? {}) as CallID[]) {
      for (const index of expandedReceivers(slice, callId)) {
        wanted.push({ key: `${callId}#${index}`, callId, index });
      }
    }
    const wantedKeys = new Set(wanted.map((w) => w.key));
    setFanoutChildren((current) => {
      let mutated = false;
      const next = new Map(current);
      for (const k of next.keys()) {
        if (!wantedKeys.has(k)) {
          next.delete(k);
          mutated = true;
        }
      }
      return mutated ? next : current;
    });

    let alive = true;
    for (const w of wanted) {
      if (fanoutChildren.has(w.key) || fanoutLoading.current.has(w.key)) continue;
      fanoutLoading.current.add(w.key);
      fetchBodyByCall(w.callId, w.index)
        .then((child) => {
          fanoutLoading.current.delete(w.key);
          if (!alive) return;
          setFanoutChildren((m) => new Map(m).set(w.key, child));
          setFanoutErrors((m) => {
            if (!m.has(w.key)) return m;
            const n = new Map(m);
            n.delete(w.key);
            return n;
          });
        })
        .catch((err: Error) => {
          fanoutLoading.current.delete(w.key);
          if (!alive) return;
          setFanoutErrors((m) => new Map(m).set(w.key, err.message));
        });
    }
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slice.fanouts]);

  // Highlight source whenever the frame changes.
  useEffect(() => {
    let alive = true;
    setHast(null);
    highlightToHast({ source: frame.source, language: frame.language, calls: frame.calls })
      .then((h) => alive && setHast(h))
      .catch((e) => {
        if (!alive) return;
        const fallback: HastRoot = {
          type: "root",
          children: [
            { type: "element", tagName: "pre", properties: { className: ["shiki", "shiki-fallback"] }, children: [
              { type: "element", tagName: "code", properties: {}, children: [
                { type: "text", value: frame.source },
              ]},
            ]},
            { type: "element", tagName: "div", properties: { className: ["frame-error"] }, children: [
              { type: "text", value: `highlight failed: ${String(e)}` },
            ]},
          ],
        };
        setHast(fallback);
      });
    return () => {
      alive = false;
    };
  }, [frame]);

  // Esc cancels selection.
  useEffect(() => {
    if (!selection) return;
    function onKey(e: KeyboardEvent) {
      if (matches("ui.dismiss", e)) setSelection(null);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selection]);

  // The card hangs below-right of the pointer, which is fine for a few lines
  // of type info and not fine once the authoring form roughly doubles its
  // height: hover anything in the lower half of the window and the save and
  // cancel buttons land past the bottom edge. It's `position: fixed`, so
  // there is nothing to scroll to reach them — the form is simply unusable
  // down there.
  //
  // Clamped after layout rather than guessed before it: the height depends on
  // the doc string, the warning, and whether the form is open. Written
  // straight to the node instead of through state — this runs after every
  // render that could change the height, and feeding a measurement back into
  // the state it measures is how a layout loop starts.
  useLayoutEffect(() => {
    const el = typeCardRef.current;
    if (!el || !typeCard) return;
    el.style.top = `${typeCard.y + 16}px`;
    const h = el.getBoundingClientRect().height;
    const top = Math.max(8, Math.min(typeCard.y + 16, window.innerHeight - h - 8));
    el.style.top = `${top}px`;
  }, [typeCard, recognizing]);

  // Esc closes the authoring form. Now that moving the mouse away no longer
  // dismisses it, it needs a way out that isn't hunting for the cancel button.
  useEffect(() => {
    if (!recognizing) return;
    function onKey(e: KeyboardEvent) {
      if (matches("ui.dismiss", e)) setRecognizing(false);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [recognizing]);

  // The recursion chain for calls inside this frame: every frame above
  // plus this one. A call resolving back into it is marked ↻.
  const chainIds = useMemo(() => new Set([...ancestors, frame.id]), [ancestors, frame.id]);

  function isRecursive(call: CallSite): boolean {
    if (call.kind === "direct" || call.kind === "ref") {
      if (call.targetId) return chainIds.has(call.targetId);
    }
    if (call.kind === "interface" || call.kind === "ref") {
      return (call.candidates ?? []).some((c) => chainIds.has(c.targetId));
    }
    return false;
  }

  function isExpandableCall(call: CallSite): boolean {
    // A reference names either one function or, when it goes through an
    // interface, any of its implementations — the same two shapes a call has.
    if (call.kind === "ref") return !!call.targetId || (call.candidates?.length ?? 0) > 0;
    if (call.kind === "direct") return !!call.targetId;
    if (call.kind === "interface") return (call.candidates?.length ?? 0) > 0;
    return false; // indirect never; fanout has its own receiver semantics
  }

  // "+1 level": expand every project call in this frame that isn't already
  // expanded — skipping recursive ones (they'd re-open an ancestor), external
  // ones (a trace shouldn't drown in stdlib/dependency bodies), and value
  // references (nothing runs there, so opening them in bulk would pad the
  // trace with bodies that execution never reaches from here). All three stay
  // individually clickable.
  const expandableNow = useMemo(
    () =>
      frame.calls
        .filter(
          (c) =>
            isExpandableCall(c) &&
            c.kind !== "ref" &&
            !c.external &&
            !isRecursive(c) &&
            !slice.expansions[c.id],
        )
        .map((c) => c.id),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [frame.calls, slice.expansions, chainIds],
  );

  const childCount =
    Object.keys(slice.expansions).length + Object.keys(slice.fanouts ?? {}).length;

  function toggleCall(call: CallSite) {
    if (call.kind === "fanout") {
      if ((call.receivers?.length ?? 0) === 0) return;
      if (isFanoutOpen(slice, call.id)) {
        store.closeFanout(path, call.id);
      } else {
        store.openFanout(path, call.id);
      }
      return;
    }
    if (call.kind === "indirect") return;
    if (call.kind === "interface" && (call.candidates?.length ?? 0) === 0) return;
    if (!isExpandableCall(call)) return;

    if (slice.expansions[call.id]) {
      store.collapse(path, call.id);
    } else {
      store.expand(path, call.id, 0);
    }
  }

  function expandAllReceivers(call: CallSite) {
    const open = new Set(expandedReceivers(slice, call.id));
    (call.receivers ?? []).forEach((_, i) => {
      if (!open.has(i)) store.expandReceiver(path, call.id, i);
    });
  }

  function chooseImpl(call: CallSite, choice: number) {
    store.setChoice(path, call.id, choice);
  }

  function closeChild(callId: CallID) {
    store.collapse(path, callId);
  }

  // Selection / fold state lives in the store under slice.folds.
  function onLineNumClick(idx: number, e: React.MouseEvent) {
    e.preventDefault();
    if (e.shiftKey && selection) {
      setSelection({ anchor: selection.anchor, head: idx });
      return;
    }
    setSelection({ anchor: idx, head: idx });
  }

  function isLineSelected(idx: number): boolean {
    if (!selection) return false;
    const lo = Math.min(selection.anchor, selection.head);
    const hi = Math.max(selection.anchor, selection.head);
    return idx >= lo && idx <= hi;
  }

  function foldSelection() {
    if (!selection) return;
    const lo = Math.min(selection.anchor, selection.head);
    const hi = Math.max(selection.anchor, selection.head);
    store.setFolds(path, mergeRange(slice.folds, [lo, hi]));
    setSelection(null);
  }

  function unfoldRange(start: number) {
    store.setFolds(
      path,
      slice.folds.filter(([s]) => s !== start),
    );
  }

  const lineAction = useMemo<(idx: number) => LineAction>(() => {
    return (idx: number): LineAction => {
      for (const [start, end] of slice.folds) {
        if (idx === start) return { kind: "fold-start", endLine: end };
        if (idx > start && idx <= end) return { kind: "skip" };
      }
      return { kind: "render" };
    };
  }, [slice.folds]);

  // Renderer hooks for hastRender.
  function renderCallSpan(
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ): ReactNode {
    const isLoading = loading.has(call.id);
    const isExpanded =
      !!slice.expansions[call.id] ||
      (call.kind === "fanout" && isFanoutOpen(slice, call.id));
    const recursive = isRecursive(call);
    const cls = [
      domProps.className as string | undefined,
      isExpanded ? "expanded" : "",
      isLoading ? "loading" : "",
      call.goroutine ? "call-site--goroutine" : "",
      recursive ? "call-site--recursive" : "",
    ]
      .filter(Boolean)
      .join(" ");
    return (
      <span
        {...domProps}
        className={cls}
        onClick={(e) => {
          e.stopPropagation();
          toggleCall(call);
        }}
      >
        {call.goroutine && (
          <span
            className="goroutine-badge"
            title="launched as a goroutine (go …)"
            aria-label="launched as a goroutine"
          >
            ⚡
          </span>
        )}
        {call.kind === "ref" && (
          <span
            className="ref-badge"
            title="named here as a value, not called — expand to read it, but execution doesn't arrive here"
            aria-label="value reference, not a call"
          >
            ⤳
          </span>
        )}
        {recursive && (
          <span
            className="recursive-badge"
            title={
              call.kind === "interface"
                ? "may recurse — a candidate implementation is already in this view's chain"
                : "recursive — this function is already in this view's chain (expand manually if you want another round)"
            }
            aria-label="recursive call"
          >
            ↻
          </span>
        )}
        {children}
      </span>
    );
  }

  // Renders the receiver list for an open fan-out call: every receiver runs
  // when the producer fires, so they're shown as siblings (not a single-choice
  // switch). Each can be expanded into its own inline frame independently.
  function renderFanout(call: CallSite): ReactNode {
    const receivers = call.receivers ?? [];
    const open = new Set(expandedReceivers(slice, call.id));
    const allOpen = receivers.length > 0 && open.size === receivers.length;
    return (
      <div key={`fan:${call.id}`} className="fanout">
        <div className="fanout-head">
          <span className="fanout-title">
            {call.fanoutKind ?? "receivers"} · {receivers.length}
          </span>
          {receivers.length > 1 && (
            <button
              className="fanout-expand-all"
              onClick={() => (allOpen ? collapseAllReceivers(call) : expandAllReceivers(call))}
            >
              {allOpen ? "collapse all" : "expand all"}
            </button>
          )}
          <button className="fanout-close" onClick={() => store.closeFanout(path, call.id)}>
            ✕
          </button>
        </div>
        <ul className="fanout-list">
          {receivers.map((r, i) => {
            const isOpen = open.has(i);
            const key = `${call.id}#${i}`;
            const child = fanoutChildren.get(key);
            const err = fanoutErrors.get(key);
            const childPath: FramePath = [...path, { callId: call.id, choice: i }];
            return (
              <li key={i} className="fanout-receiver">
                <button
                  className={`fanout-row${isOpen ? " open" : ""}`}
                  onClick={() =>
                    isOpen
                      ? store.collapseReceiver(path, call.id, i)
                      : store.expandReceiver(path, call.id, i)
                  }
                >
                  <span className="fanout-twisty">{isOpen ? "▾" : "▸"}</span>
                  <span className="fanout-label">{r.label}</span>
                  {r.confidence === "tentative" && (
                    <span className="fanout-badge" title="resolved heuristically">
                      tentative
                    </span>
                  )}
                  {r.provenance && <span className="fanout-prov">{r.provenance}</span>}
                </button>
                {isOpen && child && (
                  <div className="fanout-body">
                    <Frame
                      frame={child}
                      path={childPath}
                      onClose={() => store.collapseReceiver(path, call.id, i)}
                      ancestors={[...ancestors, frame.id]}
                    />
                  </div>
                )}
                {isOpen && err && <div className="call-error">expand failed: {err}</div>}
              </li>
            );
          })}
        </ul>
      </div>
    );
  }

  function collapseAllReceivers(call: CallSite) {
    for (const i of expandedReceivers(slice, call.id)) {
      store.collapseReceiver(path, call.id, i);
    }
  }

  // The frames expanded at these call sites. Pulled out of renderLineExtras
  // because eliding needs the same children without the source they'd
  // normally hang off — the whole point is to keep the children and drop the
  // body, so one renderer has to serve both.
  function renderChildren(calls: CallSite[]): ReactNode[] {
    const extras: ReactNode[] = [];
    for (const call of calls) {
      // A rule-marked boundary renders instead of an expansion: the point is
      // that expanding into the transport is not what you wanted.
      if (call.leaf && boundaryShown(call)) {
        // The spliced body gets its own slot in the slice rather than sharing
        // the call's. They are different subtrees: expanding the call opens
        // the callee here, and the leaf opens the implementation in another
        // repo. Sharing the id would make one collapse the other — and
        // without a slot at all the remote frame sat at a path the store had
        // no entry for, so every expansion inside it was silently dropped.
        const slot = leafSlot(call.id);
        extras.push(
          <LeafCard
            key={`leaf:${call.id}`}
            leaf={call.leaf}
            open={!!slice.expansions[slot]}
            onInline={() => store.expand(path, slot, 0)}
            onHide={() => store.collapse(path, slot)}
            onOpen={(id) => store.setSymbol(id)}
            renderFrame={(f) => (
              <Frame
                frame={f}
                path={[...path, { callId: slot, choice: 0 }]}
                ancestors={[...ancestors, frame.id]}
              />
            )}
          />,
        );
      }
      if (call.kind === "fanout") {
        if (isFanoutOpen(slice, call.id)) {
          extras.push(renderFanout(call));
        }
        continue;
      }
      const want = slice.expansions[call.id];
      const child = loadedChildren.get(call.id);
      if (want && child) {
        const childPath: FramePath = [...path, { callId: call.id, choice: want.choice }];
        extras.push(
          <InlineChild
            key={`x:${call.id}:${want.choice}`}
            call={call}
            childFrame={child}
            choice={want.choice}
            childPath={childPath}
            onChoose={(c) => chooseImpl(call, c)}
            onClose={() => closeChild(call.id)}
            ancestors={[...ancestors, frame.id]}
          />,
        );
      }
      const err = errors.get(call.id);
      if (err) {
        extras.push(
          <div key={`e:${call.id}`} className="call-error">
            expand failed: {err}
          </div>,
        );
      }
    }
    return extras;
  }

  function toggleBoundary(id: CallID) {
    setBoundariesOpen((open) => {
      const next = new Set(open);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  // A boundary is showing when it was opened, or when its far side is spliced
  // in — the card is what offers to hide that again, so it can't be gone.
  function boundaryShown(call: CallSite): boolean {
    return boundariesOpen.has(call.id) || !!slice.expansions[leafSlot(call.id)];
  }

  // The hint that lives at the end of the line: what this call crosses into,
  // said in the space past the code rather than in a block beneath it.
  function renderLineTrailer(lineIdx: number): ReactNode {
    const calls = (lineCallsCache.get(lineIdx) ?? []).filter((c) => c.leaf);
    if (calls.length === 0) return null;
    return (
      <span className="line-hints">
        {calls.map((call) => {
          const leaf = call.leaf!;
          const shown = boundaryShown(call);
          const ends = leaf.ends ?? [];
          const where =
            ends.length > 1
              ? `${ends.length} services`
              : leaf.targetPath || leaf.service || "";
          return (
            <button
              key={`hint:${call.id}`}
              type="button"
              className={`leaf-hint${shown ? " leaf-hint--open" : ""}`}
              onClick={(e) => {
                e.stopPropagation();
                toggleBoundary(call.id);
              }}
              title={[
                leaf.label || leaf.key,
                where && `→ ${where}`,
                leaf.key && `${leaf.kind ?? "key"} ${leaf.key}`,
                `recognized by rule ${leaf.rule}`,
              ]
                .filter(Boolean)
                .join(" — ")}
            >
              {leaf.label || leaf.key || "boundary"}
              <span className="leaf-hint-chevron" aria-hidden="true">
                {shown ? "⌃" : "⌄"}
              </span>
            </button>
          );
        })}
      </span>
    );
  }

  function renderLineExtras(lineIdx: number): ReactNode {
    const extras: ReactNode[] = renderChildren(lineCallsCache.get(lineIdx) ?? []);
    // The selection action bar renders right at the selection (after its
    // last line), not at the frame top — a selection made deep in a long
    // frame would otherwise have its actions scrolled out of view.
    if (selection && lineIdx === Math.max(selection.anchor, selection.head)) {
      extras.push(
        <div key="selectbar" className="frame-selectbar frame-selectbar--inline">
          <span className="frame-selectbar-info">
            {selectionCount} {selectionCount === 1 ? "line" : "lines"} selected
          </span>
          <button type="button" onClick={foldSelection} className="frame-selectbar-fold">
            fold
          </button>
          <button
            type="button"
            onClick={noteSelection}
            className="frame-selectbar-fold"
            title="attach a note to the selected line(s)"
          >
            note
          </button>
          <button
            type="button"
            onClick={() => setSelection(null)}
            className="frame-selectbar-cancel"
          >
            cancel
          </button>
          <span className="frame-selectbar-hint">shift-click to extend · esc to cancel</span>
        </div>,
      );
    }
    for (const n of notesByLine.get(lineIdx) ?? []) {
      extras.push(<NoteCard key={`n:${n.id}`} note={n} drifted={isDrifted(n)} />);
    }
    if (
      composing &&
      (composing.kind === "after-line" || composing.kind === "range") &&
      (composing.endLine ?? 0) - frame.startLine === lineIdx
    ) {
      extras.push(
        <NoteComposer key="compose" anchor={composing} onDone={() => setComposing(null)} />,
      );
    }
    return extras.length ? <Fragment key={`extras:${lineIdx}`}>{extras}</Fragment> : null;
  }

  const lineCallsCache = useMemo(() => buildLineCalls(frame), [frame]);

  function renderLineGutter(lineIdx: number): ReactNode {
    const fileLineNum = frame.startLine + lineIdx;
    const selected = isLineSelected(lineIdx);
    return (
      <button
        type="button"
        className={`line-num${selected ? " line-num--selected" : ""}`}
        onClick={(e) => onLineNumClick(lineIdx, e)}
        onMouseDown={(e) => e.preventDefault()}
        title={`line ${fileLineNum} — click to select, shift-click to extend`}
      >
        {fileLineNum}
      </button>
    );
  }

  function renderFoldPlaceholder(startLine: number, endLine: number): ReactNode {
    const count = endLine - startLine + 1;
    const fileStart = frame.startLine + startLine;
    const fileEnd = frame.startLine + endLine;
    return (
      <button
        type="button"
        className="fold-placeholder"
        onClick={() => unfoldRange(startLine)}
        title={`unfold lines ${fileStart}–${fileEnd}`}
      >
        ··· {count} {count === 1 ? "line" : "lines"} hidden ({fileStart}–{fileEnd})
      </button>
    );
  }

  const selectionCount = selection
    ? Math.abs(selection.head - selection.anchor) + 1
    : 0;

  // Diff tinting: a whole "added" frame tints every line; a "modified" frame
  // tints only its changed lines (0-based indices from the backend).
  const diff = frame.diff;
  const addedSet =
    diff?.status === "modified" && diff.addedLines?.length
      ? new Set(diff.addedLines)
      : null;
  const diffClass: ((idx: number) => string | undefined) | undefined =
    diff?.status === "added"
      ? () => "line-row--added"
      : addedSet
        ? (idx) => (addedSet.has(idx) ? "line-row--added" : undefined)
        : undefined;
  // Compose diff tinting with range-note tinting.
  const lineClass: ((idx: number) => string | undefined) | undefined =
    diffClass || notedLines.size > 0
      ? (idx) => {
          const parts = [
            diffClass?.(idx),
            notedLines.has(idx) ? "line-row--noted" : undefined,
          ].filter(Boolean);
          return parts.length ? parts.join(" ") : undefined;
        }
      : undefined;

  // Eliding only means anything for a frame with something expanded inside
  // it. On a leaf there is nothing to keep, so hiding the body would just be
  // closing the frame the long way round — and offering a control that
  // silently does what "×" already does is worse than not offering it.
  const hasOpenChildren =
    Object.keys(slice.expansions).length > 0 ||
    Object.keys(slice.fanouts ?? {}).length > 0;

  if (slice.elided && hasOpenChildren) {
    // The body is gone but the hop is not: saying "through X" keeps the chain
    // honest, because the alternative is a view in which the caller appears
    // to call the grandchild directly. That would be the same class of lie
    // the rest of the tool works to avoid.
    return (
      <div
        className="frame frame--elided"
        style={{ "--depth-color": depthColor(depth) } as React.CSSProperties}
        data-frame-key={pathKey(path)}
        data-frame-title={frameTitle(frame)}
        data-frame-loc={`${shortPath(frame.file)}:${frame.startLine}`}
      >
        <div className="elide-bar">
          <button
            type="button"
            className="elide-restore"
            onClick={() => store.setElided(path, false)}
            title="show this frame's body again"
          >
            ⋯
          </button>
          <span className="elide-through">
            through <b>{frameTitle(frame)}</b>
          </span>
          <button
            type="button"
            className="frame-loc frame-loc--link"
            title="open in editor"
            onClick={() => openInEditor(frame.file, frame.startLine).catch(() => {})}
          >
            {shortPath(frame.file)}:{frame.startLine}
          </button>
          {onClose && (
            <button className="frame-close" onClick={onClose} aria-label="collapse">
              ×
            </button>
          )}
        </div>
        {renderChildren(frame.calls)}
      </div>
    );
  }

  return (
    <div
      className={`frame${settings.depthRails ? " frame--railed" : ""}`}
      // The rail color doubles as the header accent; same palette as the
      // pinned sticky-header stack so both cues read as one system.
      style={{ "--depth-color": depthColor(depth) } as React.CSSProperties}
      data-frame-key={pathKey(path)}
      // Read by StickyHeaders to render the pinned call-chain stack.
      data-frame-title={frameTitle(frame)}
      data-frame-loc={`${frameLoc(frame)}:${frame.startLine}`}
    >
      <header className="frame-header">
        <button
          type="button"
          className={`frame-bookmark${bookmarks.isBookmarked(frame.id) ? " frame-bookmark--on" : ""}`}
          onClick={() =>
            bookmarks.toggle({
              targetId: frame.id,
              title: frameTitle(frame),
              file: frame.file,
              line: frame.startLine,
            })
          }
          title={bookmarks.isBookmarked(frame.id) ? "remove bookmark" : "bookmark this function"}
          aria-label="toggle bookmark"
        >
          {bookmarks.isBookmarked(frame.id) ? "★" : "☆"}
        </button>
        <span className="frame-title">{frameTitle(frame)}</span>
        {settings.depthRuler && (
          <span className="frame-depth" title={`nesting depth ${depth}`}>
            {depth}
          </span>
        )}
        {frame.diff && frame.diff.status !== "unchanged" && (
          <span
            className={`frame-diff-badge frame-diff-badge--${frame.diff.status}`}
            title={
              frame.diff.status === "added"
                ? "new on this branch (not in the diff base)"
                : "changed on this branch"
            }
          >
            {frame.diff.status}
          </span>
        )}
        {!frame.id.startsWith("file:") && (
          <button
            type="button"
            className={`frame-callers${callersOpen ? " frame-callers--open" : ""}`}
            onClick={() => setCallersOpen((v) => !v)}
            title="show callers — pick one to splice it above (re-roots the view)"
          >
            ▲ callers
          </button>
        )}
        {onZoomOut && (
          <button
            type="button"
            className="frame-zoom-out"
            onClick={onZoomOut}
            title="zoom out to the service (alt+↑) — shows which entrypoints reach this frame"
          >
            ▴ service
          </button>
        )}
        {isFileFrame && (
          <>
            <button
              type="button"
              className="frame-tool"
              onClick={() => setComposing({ file: frame.file, kind: "file-start" })}
              title="add a note at the top of this file"
            >
              note @ top
            </button>
            <button
              type="button"
              className="frame-tool"
              onClick={() => setComposing({ file: frame.file, kind: "file-end" })}
              title="add a note at the end of this file"
            >
              note @ end
            </button>
          </>
        )}
        {expandableNow.length > 0 && (
          <button
            type="button"
            className="frame-tool"
            onClick={() => store.expandMany(path, expandableNow)}
            title={`expand all ${expandableNow.length} unexpanded project calls in this frame one level (recursive and stdlib/dependency calls are skipped)`}
          >
            +1 level
          </button>
        )}
        {childCount > 0 && (
          <button
            type="button"
            className="frame-tool"
            onClick={() => store.clearChildren(path)}
            title="collapse everything expanded inside this frame"
          >
            collapse all
          </button>
        )}
        {/* Only offered on an intermediate frame — one with something open
            inside it. Eliding a leaf would just be closing it. */}
        {hasOpenChildren && path.length > 0 && (
          <button
            type="button"
            className="frame-elide"
            onClick={() => store.setElided(path, true)}
            title="hide this frame's body and keep what it expands into — brings the caller and the frames below it together"
          >
            elide
          </button>
        )}
        <button
          type="button"
          className="frame-loc frame-loc--link"
          title="open in editor"
          onClick={() => openInEditor(frame.file, frame.startLine).catch(() => {})}
        >
          {frameLoc(frame)}:{frame.startLine}
        </button>
        {onClose && (
          <button className="frame-close" onClick={onClose} aria-label="collapse">
            ×
          </button>
        )}
      </header>
      {callersOpen && (
        <CallersPanel frame={frame} path={path} onClose={() => setCallersOpen(false)} />
      )}
      {isFileFrame && (fileStartNotes.length > 0 || composing?.kind === "file-start") && (
        <div className="frame-notes">
          {fileStartNotes.map((n) => (
            <NoteCard key={n.id} note={n} />
          ))}
          {composing?.kind === "file-start" && (
            <NoteComposer anchor={composing} onDone={() => setComposing(null)} />
          )}
        </div>
      )}
      <div className="frame-body">
        {hast ? (
          <div
            className="frame-source"
            onMouseMove={onSourceMouseMove}
            onMouseLeave={onSourceMouseLeave}
          >
            {renderHast({
              hast,
              source: frame.source,
              calls: frame.calls,
              renderCallSpan,
              renderLineExtras: (idx) => renderLineExtras(idx),
              renderLineTrailer: (idx) => renderLineTrailer(idx),
              renderLineGutter,
              renderFoldPlaceholder,
              lineAction,
              lineClass,
            })}
          </div>
        ) : (
          <div className="frame-loading">loading…</div>
        )}
      </div>
      {isFileFrame && (fileEndNotes.length > 0 || composing?.kind === "file-end") && (
        <div className="frame-notes">
          {fileEndNotes.map((n) => (
            <NoteCard key={n.id} note={n} />
          ))}
          {composing?.kind === "file-end" && (
            <NoteComposer anchor={composing} onDone={() => setComposing(null)} />
          )}
        </div>
      )}
      {typeCard && (
        <div
          ref={typeCardRef}
          className="type-card"
          style={{ left: typeCard.x + 12, top: typeCard.y + 16 }}
          // Reaching the card means crossing a strip of code, and everything
          // crossed on the way armed something. Arriving cancels all of it.
          onMouseEnter={cancelHover}
          // Leaving gets the same grace period as leaving the source, so
          // clipping a corner on the way to a button doesn't cost the card.
          // While the form is open, nothing here closes it: it holds typed
          // input, and mouse position is not consent to discard that.
          onMouseLeave={() => {
            if (recognizing) return;
            scheduleHide();
          }}
        >
          <div className="type-card-head">
            <span className="type-card-kind">{typeCard.info.kind}</span>
            <span className="type-card-name">{typeCard.info.name}</span>
          </div>
          {typeCard.info.type && <div className="type-card-type">{typeCard.info.type}</div>}
          {typeCard.info.definition && (
            <pre className="type-card-def">{typeCard.info.definition}</pre>
          )}
          {typeCard.info.doc && <div className="type-card-doc">{typeCard.info.doc}</div>}
          {typeCard.info.definedAt && (
            <button
              type="button"
              className="type-card-loc"
              onClick={() => openDefinition(typeCard.info)}
              title={typeCard.info.targetId ? "open as root frame" : "open in editor"}
            >
              {shortDefined(typeCard.info.definedAt)}
            </button>
          )}
          {/* What already claims this call. Offering to recognize a call that
              three rules match, without saying so, invites a fourth rule that
              duplicates one you have — and leaves "why is there an edge here"
              unanswerable at the one place you're looking. Clicking a rule
              opens it in the recognizers panel. */}
          {typeCard.info.rules && typeCard.info.rules.length > 0 && (
            <div className="type-card-rules">
              <span className="type-card-rules-label">recognized by</span>
              {typeCard.info.rules.map((id) => (
                <button
                  key={id}
                  type="button"
                  className="type-card-rule"
                  onClick={() => {
                    closeTypeCard();
                    openRules(id);
                  }}
                  title={`show ${id} in the recognizers panel`}
                >
                  {id}
                </button>
              ))}
            </div>
          )}
          {/* Author a recognizer from the call you're looking at. The package
              and receiver are already resolved to render this card, so the
              match writes itself — only which argument holds the key has to
              be asked, because that's the part nobody else knows. */}
          {recognizing ? (
            <RecognizeCall
              info={typeCard.info}
              displayName={typeCard.info.name}
              onDone={closeTypeCard}
              onCancel={() => setRecognizing(false)}
            />
          ) : (
            <button
              type="button"
              className="type-card-recognize"
              onClick={() => setRecognizing(true)}
              title="teach unfold that this call shape is a platform edge"
            >
              {typeCard.info.rules?.length ? "recognize as something else…" : "recognize as…"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

interface InlineChildProps {
  call: CallSite;
  childFrame: FrameT;
  choice: number;
  childPath: FramePath;
  onChoose: (choice: number) => void;
  onClose: () => void;
  ancestors: TargetID[];
}

function InlineChild({
  call,
  childFrame,
  choice,
  childPath,
  onChoose,
  onClose,
  ancestors,
}: InlineChildProps) {
  const candidates = call.candidates ?? [];
  // A reference through an interface picks among implementations exactly as a
  // call through one does, so it gets the same switcher.
  const showSwitcher =
    (call.kind === "interface" || call.kind === "ref") && candidates.length > 1;
  return (
    <div className="inline-child">
      {showSwitcher && (
        <div className="impl-switcher" onClick={(e) => e.stopPropagation()}>
          <span className="impl-switcher-label">impl:</span>
          <select value={choice} onChange={(e) => onChoose(Number(e.target.value))}>
            {candidates.map((c, i) => (
              <option key={c.targetId} value={i}>
                {c.label}
              </option>
            ))}
          </select>
          <span className="impl-switcher-count">
            {choice + 1} / {candidates.length}
          </span>
        </div>
      )}
      <Frame frame={childFrame} path={childPath} onClose={onClose} ancestors={ancestors} />
    </div>
  );
}

function mergeRange(current: [number, number][], add: [number, number]): [number, number][] {
  const all = [...current, add].sort((a, b) => a[0] - b[0]);
  const out: [number, number][] = [];
  for (const r of all) {
    const last = out[out.length - 1];
    if (last && r[0] <= last[1] + 1) {
      last[1] = Math.max(last[1], r[1]);
    } else {
      out.push([r[0], r[1]]);
    }
  }
  return out;
}

// leafSlot is the slice key for a leaf's spliced remote body. It is deliberately
// not a call id any engine emits: the frame that owns this slot has no call
// site for the far end — that body was resolved by key, not by call — so
// nothing may try to fetch it as one.
function leafSlot(callId: CallID): CallID {
  return `${callId}#leaf` as CallID;
}

function buildLineCalls(frame: FrameT): Map<number, CallSite[]> {
  const map = new Map<number, CallSite[]>();
  for (const c of frame.calls) {
    const idx = lineForOffset(frame.source, c.spanStart);
    const list = map.get(idx) ?? [];
    list.push(c);
    map.set(idx, list);
  }
  for (const list of map.values()) list.sort((a, b) => a.spanStart - b.spanStart);
  return map;
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}

function frameTitle(frame: FrameT): string {
  return frame.title && frame.title.trim() ? frame.title : prettyName(frame.id);
}

// Cross-browser caret position from screen coordinates (for hover→offset).
function caretFromPoint(x: number, y: number): { node: Node; offset: number } | null {
  const doc = document as Document & {
    caretRangeFromPoint?: (x: number, y: number) => Range | null;
    caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
  };
  if (doc.caretRangeFromPoint) {
    const r = doc.caretRangeFromPoint(x, y);
    return r ? { node: r.startContainer, offset: r.startOffset } : null;
  }
  if (doc.caretPositionFromPoint) {
    const p = doc.caretPositionFromPoint(x, y);
    return p ? { node: p.offsetNode, offset: p.offset } : null;
  }
  return null;
}

function shortDefined(at: string): string {
  const i = at.lastIndexOf(":");
  const file = i > 0 ? at.slice(0, i) : at;
  const line = i > 0 ? at.slice(i + 1) : "";
  const base = file.slice(file.lastIndexOf("/") + 1);
  return line ? `${base}:${line}` : base;
}

function prettyName(id: string): string {
  const parts = id.split("/");
  if (parts.length <= 2) return id;
  return ".../" + parts.slice(-2).join("/");
}

// Where the frame is, as short as it can be while still answering "which
// service". In a workspace the engine supplies a service-qualified path; a
// single repo has no services to distinguish, so the tail of the path is
// enough and stays as it was.
function frameLoc(frame: FrameT): string {
  return frame.relPath || shortPath(frame.file);
}

function shortPath(p: string): string {
  const idx = p.lastIndexOf("/");
  if (idx < 0) return p;
  const slash2 = p.lastIndexOf("/", idx - 1);
  if (slash2 < 0) return p.slice(idx + 1);
  return p.slice(slash2 + 1);
}
