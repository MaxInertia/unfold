import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import type { Root as HastRoot } from "hast";
import { fetchBodyByCall } from "./api";
import { highlightToHast } from "./highlight";
import { renderHast } from "./hastRender";
import type { CallID, CallSite, Frame as FrameT } from "./types";

interface FrameProps {
  frame: FrameT;
  onClose?: () => void;
}

interface ExpandedChild {
  frame: FrameT;
  choice: number; // candidate index, only meaningful for interface kind
}

export function Frame({ frame, onClose }: FrameProps) {
  const [hast, setHast] = useState<HastRoot | null>(null);
  const [expanded, setExpanded] = useState<Map<CallID, ExpandedChild>>(new Map());
  const [loading, setLoading] = useState<Set<CallID>>(new Set());
  const [errors, setErrors] = useState<Map<CallID, string>>(new Map());

  // Highlight source whenever the frame changes.
  useEffect(() => {
    let alive = true;
    setHast(null);
    highlightToHast({ source: frame.source, language: frame.language, calls: frame.calls })
      .then((h) => alive && setHast(h))
      .catch((e) => {
        if (!alive) return;
        // If highlighting fails, fall back to a plain-text root so the
        // rest of the UI still works.
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

  function expandCall(call: CallSite, choice: number) {
    setLoading((s) => new Set(s).add(call.id));
    setErrors((m) => {
      const n = new Map(m);
      n.delete(call.id);
      return n;
    });
    fetchBodyByCall(call.id, choice)
      .then((child) => {
        setLoading((s) => {
          const n = new Set(s);
          n.delete(call.id);
          return n;
        });
        setExpanded((m) => new Map(m).set(call.id, { frame: child, choice }));
      })
      .catch((err: Error) => {
        setLoading((s) => {
          const n = new Set(s);
          n.delete(call.id);
          return n;
        });
        setErrors((m) => new Map(m).set(call.id, err.message));
      });
  }

  function toggleCall(call: CallSite) {
    if (call.kind === "indirect") return;
    if (call.kind === "interface" && (call.candidates?.length ?? 0) === 0) return;
    if (call.kind === "direct" && !call.targetId) return;

    if (expanded.has(call.id)) {
      setExpanded((m) => {
        const n = new Map(m);
        n.delete(call.id);
        return n;
      });
      return;
    }
    if (loading.has(call.id)) return;
    expandCall(call, 0);
  }

  function chooseImpl(call: CallSite, choice: number) {
    expandCall(call, choice);
  }

  function closeChild(cid: CallID) {
    setExpanded((m) => {
      const n = new Map(m);
      n.delete(cid);
      return n;
    });
  }

  // Renderer hooks for hastRender.
  function renderCallSpan(
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ): ReactNode {
    const isLoading = loading.has(call.id);
    const isExpanded = expanded.has(call.id);
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

  function renderLineExtras(lineIdx: number, lineSource: string): ReactNode {
    // Intentionally a slot per line — multiple expanded calls on one
    // line stack in source order.
    const extras: ReactNode[] = [];
    // Find calls whose start offset falls on this line, in spanStart
    // order so siblings render predictably.
    const calls = lineCallsCache.get(lineIdx);
    if (!calls) return null;
    for (const call of calls) {
      const child = expanded.get(call.id);
      if (child) {
        extras.push(
          <InlineChild
            key={`x:${call.id}:${child.choice}`}
            call={call}
            child={child}
            indent={leadingIndent(lineSource)}
            onChoose={(c) => chooseImpl(call, c)}
            onClose={() => closeChild(call.id)}
          />,
        );
      }
      const err = errors.get(call.id);
      if (err) {
        extras.push(
          <div
            key={`e:${call.id}`}
            className="call-error"
            style={{ marginLeft: leadingIndent(lineSource) }}
          >
            expand failed: {err}
          </div>,
        );
      }
    }
    return extras.length ? <Fragment key={`extras:${lineIdx}`}>{extras}</Fragment> : null;
  }

  // Pre-compute calls-per-line and the source of each line so
  // renderLineExtras can place children with the correct indent.
  const lineCallsCache = useMemo(() => buildLineCalls(frame), [frame]);
  const lineSources = useMemo(() => frame.source.split("\n"), [frame]);

  return (
    <div className="frame">
      <header className="frame-header">
        <span className="frame-title">{prettyName(frame.id)}</span>
        <span className="frame-loc">
          {shortPath(frame.file)}:{frame.startLine}
        </span>
        {onClose && (
          <button className="frame-close" onClick={onClose} aria-label="collapse">
            ×
          </button>
        )}
      </header>
      <div className="frame-body">
        {hast ? (
          <div className="frame-source">
            {renderHast({
              hast,
              source: frame.source,
              calls: frame.calls,
              renderCallSpan,
              renderLineExtras: (idx) =>
                renderLineExtras(idx, lineSources[idx] ?? ""),
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
  child: ExpandedChild;
  indent: string;
  onChoose: (choice: number) => void;
  onClose: () => void;
}

function InlineChild({ call, child, indent, onChoose, onClose }: InlineChildProps) {
  const candidates = call.candidates ?? [];
  const showSwitcher = call.kind === "interface" && candidates.length > 1;
  return (
    <div className="inline-child" style={{ marginLeft: indent }}>
      {showSwitcher && (
        <div className="impl-switcher" onClick={(e) => e.stopPropagation()}>
          <span className="impl-switcher-label">impl:</span>
          <select
            value={child.choice}
            onChange={(e) => onChoose(Number(e.target.value))}
          >
            {candidates.map((c, i) => (
              <option key={c.targetId} value={i}>
                {c.label}
              </option>
            ))}
          </select>
          <span className="impl-switcher-count">
            {child.choice + 1} / {candidates.length}
          </span>
        </div>
      )}
      <Frame frame={child.frame} onClose={onClose} />
    </div>
  );
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

function leadingIndent(line: string): string {
  // Returns just the whitespace prefix (tabs become tab-character; CSS
  // margin-left of "<tab>..." won't render — translate to em-equivalent.)
  let i = 0;
  while (i < line.length && (line[i] === "\t" || line[i] === " ")) i++;
  const ws = line.slice(0, i);
  // Approximate: tab = 4 chars, space = 1 char, of em-width.
  let chars = 0;
  for (const c of ws) chars += c === "\t" ? 4 : 1;
  return `${chars}ch`;
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
