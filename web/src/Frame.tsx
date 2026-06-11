import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { Root as HastRoot } from "hast";
import { fetchBodyByCall, fetchTypeInfo, openInEditor } from "./api";
import { highlightToHast } from "./highlight";
import { renderHast, type LineAction } from "./hastRender";
import type { CallID, CallSite, Frame as FrameT, TargetID, TypeInfo } from "./types";
import {
  expandedReceivers,
  isFanoutOpen,
  pathKey,
  useFrameSlice,
  useViewStore,
  type FramePath,
} from "./viewState";
import { useBookmarks } from "./bookmarks";
import { CallersPanel } from "./Callers";
import { depthColor } from "./StickyHeaders";
import { useSettings } from "./settings";

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
}

export function Frame({ frame, path, onClose, ancestors = [] }: FrameProps) {
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
  const settings = useSettings();
  const depth = path.length;
  const [typeCard, setTypeCard] = useState<{ x: number; y: number; info: TypeInfo } | null>(null);
  const hoverRef = useRef({ offset: -1, showTimer: 0, hideTimer: 0 });

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

  function onSourceMouseMove(e: React.MouseEvent) {
    if (selection) return; // don't fight a line selection
    const off = offsetAtPoint(e.clientX, e.clientY);
    if (off == null || off === hoverRef.current.offset) return;
    hoverRef.current.offset = off;
    const x = e.clientX;
    const y = e.clientY;
    window.clearTimeout(hoverRef.current.showTimer);
    hoverRef.current.showTimer = window.setTimeout(() => {
      fetchTypeInfo(frame.id, off)
        .then((info) => setTypeCard(info ? { x, y, info } : null))
        .catch(() => setTypeCard(null));
    }, 250);
  }

  function onSourceMouseLeave() {
    window.clearTimeout(hoverRef.current.showTimer);
    hoverRef.current.offset = -1;
    hoverRef.current.hideTimer = window.setTimeout(() => setTypeCard(null), 200);
  }

  function openDefinition(info: TypeInfo) {
    setTypeCard(null);
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
    for (const cid of wantedIds) {
      const want = slice.expansions[cid];
      if (!want) continue;
      const loaded = loadedChildren.get(cid);
      // Need to (re)fetch if not loaded OR loaded with stale choice.
      if (loaded && (loaded as { __choice?: number }).__choice === want.choice) continue;
      if (loading.has(cid)) continue;
      setLoading((s) => new Set(s).add(cid));
      fetchBodyByCall(cid, want.choice)
        .then((child) => {
          if (!alive) return;
          // Tag with the choice so we can detect choice changes.
          (child as { __choice?: number }).__choice = want.choice;
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
  }, [slice.expansions]);

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
      if (e.key === "Escape") setSelection(null);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selection]);

  // The recursion chain for calls inside this frame: every frame above
  // plus this one. A call resolving back into it is marked ↻.
  const chainIds = useMemo(() => new Set([...ancestors, frame.id]), [ancestors, frame.id]);

  function isRecursive(call: CallSite): boolean {
    if (call.kind === "direct") return !!call.targetId && chainIds.has(call.targetId);
    if (call.kind === "interface") {
      return (call.candidates ?? []).some((c) => chainIds.has(c.targetId));
    }
    return false;
  }

  function isExpandableCall(call: CallSite): boolean {
    if (call.kind === "direct") return !!call.targetId;
    if (call.kind === "interface") return (call.candidates?.length ?? 0) > 0;
    return false; // indirect never; fanout has its own receiver semantics
  }

  // "+1 level": expand every project call in this frame that isn't already
  // expanded — skipping recursive ones (they'd re-open an ancestor) and
  // external ones (a trace shouldn't drown in stdlib/dependency bodies).
  // Both stay individually clickable.
  const expandableNow = useMemo(
    () =>
      frame.calls
        .filter(
          (c) =>
            isExpandableCall(c) &&
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
    if (call.kind === "direct" && !call.targetId) return;

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

  function renderLineExtras(lineIdx: number): ReactNode {
    const extras: ReactNode[] = [];
    const calls = lineCallsCache.get(lineIdx);
    if (!calls) return null;
    for (const call of calls) {
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

  const hasSelection = selection !== null;
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
  const lineClass: ((idx: number) => string | undefined) | undefined =
    diff?.status === "added"
      ? () => "line-row--added"
      : addedSet
        ? (idx) => (addedSet.has(idx) ? "line-row--added" : undefined)
        : undefined;

  return (
    <div
      className={`frame${settings.depthRails ? " frame--railed" : ""}`}
      // The rail color doubles as the header accent; same palette as the
      // pinned sticky-header stack so both cues read as one system.
      style={{ "--depth-color": depthColor(depth) } as React.CSSProperties}
      data-frame-key={pathKey(path)}
      // Read by StickyHeaders to render the pinned call-chain stack.
      data-frame-title={frameTitle(frame)}
      data-frame-loc={`${shortPath(frame.file)}:${frame.startLine}`}
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
      </header>
      {callersOpen && (
        <CallersPanel frame={frame} path={path} onClose={() => setCallersOpen(false)} />
      )}
      {hasSelection && (
        <div className="frame-selectbar">
          <span className="frame-selectbar-info">
            {selectionCount} {selectionCount === 1 ? "line" : "lines"} selected
          </span>
          <button type="button" onClick={foldSelection} className="frame-selectbar-fold">
            fold
          </button>
          <button
            type="button"
            onClick={() => setSelection(null)}
            className="frame-selectbar-cancel"
          >
            cancel
          </button>
          <span className="frame-selectbar-hint">shift-click to extend · esc to cancel</span>
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
      {typeCard && (
        <div
          className="type-card"
          style={{ left: typeCard.x + 12, top: typeCard.y + 16 }}
          onMouseEnter={() => window.clearTimeout(hoverRef.current.hideTimer)}
          onMouseLeave={() => setTypeCard(null)}
        >
          <div className="type-card-head">
            <span className="type-card-kind">{typeCard.info.kind}</span>
            <span className="type-card-name">{typeCard.info.name}</span>
          </div>
          {typeCard.info.type && <div className="type-card-type">{typeCard.info.type}</div>}
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
  const showSwitcher = call.kind === "interface" && candidates.length > 1;
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

function shortPath(p: string): string {
  const idx = p.lastIndexOf("/");
  if (idx < 0) return p;
  const slash2 = p.lastIndexOf("/", idx - 1);
  if (slash2 < 0) return p.slice(idx + 1);
  return p.slice(slash2 + 1);
}
