import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import type { Root as HastRoot } from "hast";
import { fetchBodyByCall } from "./api";
import { highlightToHast } from "./highlight";
import { renderHast, type LineAction } from "./hastRender";
import type { CallID, CallSite, Frame as FrameT } from "./types";
import { pathKey, useFrameSlice, useViewStore, type FramePath } from "./viewState";
import { useBookmarks } from "./bookmarks";

export interface FoldRange {
  start: number;
  end: number;
}

interface FrameProps {
  frame: FrameT;
  path: FramePath;
  onClose?: () => void;
}

export function Frame({ frame, path, onClose }: FrameProps) {
  const store = useViewStore();
  const slice = useFrameSlice(path);
  const bookmarks = useBookmarks();
  const [hast, setHast] = useState<HastRoot | null>(null);
  // Loaded child frames are component-local; the URL only persists the
  // intent (which calls are expanded with what choice).
  const [loadedChildren, setLoadedChildren] = useState<Map<CallID, FrameT>>(new Map());
  const [loading, setLoading] = useState<Set<CallID>>(new Set());
  const [errors, setErrors] = useState<Map<CallID, string>>(new Map());
  const [selection, setSelection] = useState<{ anchor: number; head: number } | null>(null);

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

  function toggleCall(call: CallSite) {
    if (call.kind === "indirect") return;
    if (call.kind === "interface" && (call.candidates?.length ?? 0) === 0) return;
    if (call.kind === "direct" && !call.targetId) return;

    if (slice.expansions[call.id]) {
      store.collapse(path, call.id);
    } else {
      store.expand(path, call.id, 0);
    }
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
    const isExpanded = !!slice.expansions[call.id];
    const cls = [
      domProps.className as string | undefined,
      isExpanded ? "expanded" : "",
      isLoading ? "loading" : "",
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
        {children}
      </span>
    );
  }

  function renderLineExtras(lineIdx: number): ReactNode {
    const extras: ReactNode[] = [];
    const calls = lineCallsCache.get(lineIdx);
    if (!calls) return null;
    for (const call of calls) {
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

  return (
    <div className="frame" data-frame-key={pathKey(path)}>
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
        <span className="frame-loc">
          {shortPath(frame.file)}:{frame.startLine}
        </span>
        {onClose && (
          <button className="frame-close" onClick={onClose} aria-label="collapse">
            ×
          </button>
        )}
      </header>
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
          <div className="frame-source">
            {renderHast({
              hast,
              source: frame.source,
              calls: frame.calls,
              renderCallSpan,
              renderLineExtras: (idx) => renderLineExtras(idx),
              renderLineGutter,
              renderFoldPlaceholder,
              lineAction,
            })}
          </div>
        ) : (
          <div className="frame-loading">loading…</div>
        )}
      </div>
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
}

function InlineChild({
  call,
  childFrame,
  choice,
  childPath,
  onChoose,
  onClose,
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
      <Frame frame={childFrame} path={childPath} onClose={onClose} />
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
