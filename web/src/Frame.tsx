import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { fetchBodyByCall } from "./api";
import { highlightCode } from "./highlight";
import type { CallID, CallSite, Frame as FrameT } from "./types";

interface FrameProps {
  frame: FrameT;
  onClose?: () => void;
}

interface ExpandedChild {
  frame: FrameT;
  choice: number; // selected candidate index, only meaningful for interface kind
}

export function Frame({ frame, onClose }: FrameProps) {
  const [html, setHtml] = useState<string | null>(null);
  const [callElements, setCallElements] = useState<Map<CallID, HTMLElement>>(new Map());
  const [expanded, setExpanded] = useState<Map<CallID, ExpandedChild>>(new Map());
  const [loading, setLoading] = useState<Set<CallID>>(new Set());
  const [errors, setErrors] = useState<Map<CallID, string>>(new Map());
  const containerRef = useRef<HTMLDivElement>(null);

  // Highlight source whenever the frame changes.
  useEffect(() => {
    let alive = true;
    setHtml(null);
    highlightCode({ source: frame.source, language: frame.language, calls: frame.calls })
      .then((h) => alive && setHtml(h))
      .catch((e) => alive && setHtml(`<pre>highlight error: ${escapeHTML(String(e))}</pre>`));
    return () => {
      alive = false;
    };
  }, [frame]);

  // After the HTML is in the DOM, look up the per-call span elements so
  // we can render portals into them.
  useLayoutEffect(() => {
    if (!html || !containerRef.current) {
      setCallElements(new Map());
      return;
    }
    const map = new Map<CallID, HTMLElement>();
    for (const c of frame.calls) {
      const sel = `[data-call-id="${cssEscape(c.id)}"]`;
      const el = containerRef.current.querySelector(sel) as HTMLElement | null;
      if (el) map.set(c.id, el);
    }
    setCallElements(map);
  }, [html, frame.calls]);

  // Toggle expanded/loading classes on call-site spans.
  useEffect(() => {
    callElements.forEach((el, cid) => {
      el.classList.toggle("expanded", expanded.has(cid));
      el.classList.toggle("loading", loading.has(cid));
    });
  }, [callElements, expanded, loading]);

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

  function onClickBody(e: React.MouseEvent) {
    const el = (e.target as HTMLElement).closest("[data-call-id]") as HTMLElement | null;
    if (!el) return;
    const callId = el.getAttribute("data-call-id") as CallID;
    const kind = el.getAttribute("data-call-kind");

    e.stopPropagation();

    const call = frame.calls.find((c) => c.id === callId);
    if (!call) return;

    // Indirect calls aren't expandable.
    if (kind === "indirect") return;

    // Interface calls require known candidates.
    if (kind === "interface" && (call.candidates?.length ?? 0) === 0) return;

    if (expanded.has(callId)) {
      setExpanded((m) => {
        const n = new Map(m);
        n.delete(callId);
        return n;
      });
      return;
    }
    if (loading.has(callId)) return;
    expandCall(call, 0);
  }

  function chooseImpl(call: CallSite, choice: number) {
    expandCall(call, choice);
  }

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
      <div className="frame-body" ref={containerRef} onClick={onClickBody}>
        {html ? (
          <div className="frame-source" dangerouslySetInnerHTML={{ __html: html }} />
        ) : (
          <div className="frame-loading">loading…</div>
        )}
      </div>
      {/* Portals: render each expanded child Frame inside its call-site span. */}
      {Array.from(expanded.entries()).map(([cid, child]) => {
        const target = callElements.get(cid);
        if (!target) return null;
        const call = frame.calls.find((c) => c.id === cid);
        if (!call) return null;
        return createPortal(
          <ExpandedFrame
            key={`${cid}:${child.choice}`}
            call={call}
            child={child}
            onChoose={(c) => chooseImpl(call, c)}
            onClose={() =>
              setExpanded((m) => {
                const n = new Map(m);
                n.delete(cid);
                return n;
              })
            }
          />,
          target,
        );
      })}
      {/* Inline errors per call. */}
      {Array.from(errors.entries()).map(([cid, msg]) => {
        const target = callElements.get(cid);
        if (!target) return null;
        return createPortal(<div className="call-error">expand failed: {msg}</div>, target);
      })}
    </div>
  );
}

interface ExpandedFrameProps {
  call: CallSite;
  child: ExpandedChild;
  onChoose: (choice: number) => void;
  onClose: () => void;
}

function ExpandedFrame({ call, child, onChoose, onClose }: ExpandedFrameProps) {
  const candidates = call.candidates ?? [];
  const showSwitcher = call.kind === "interface" && candidates.length > 1;

  return (
    <div className="expanded">
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

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] as string,
  );
}

function cssEscape(s: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") return CSS.escape(s);
  return s.replace(/([!"#$%&'()*+,./:;<=>?@[\\\]^`{|}~])/g, "\\$1");
}
