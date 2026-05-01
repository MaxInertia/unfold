import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { fetchBodyByCall } from "./api";
import { highlightCode } from "./highlight";
import type { CallID, Frame as FrameT } from "./types";

interface FrameProps {
  frame: FrameT;
  onClose?: () => void;
}

export function Frame({ frame, onClose }: FrameProps) {
  const [html, setHtml] = useState<string | null>(null);
  const [callElements, setCallElements] = useState<Map<CallID, HTMLElement>>(new Map());
  const [expanded, setExpanded] = useState<Map<CallID, FrameT>>(new Map());
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

  // Toggle an `expanded` class on call-site spans so CSS can style them.
  useEffect(() => {
    callElements.forEach((el, cid) => {
      if (expanded.has(cid)) el.classList.add("expanded");
      else el.classList.remove("expanded");
      if (loading.has(cid)) el.classList.add("loading");
      else el.classList.remove("loading");
    });
  }, [callElements, expanded, loading]);

  function onClickBody(e: React.MouseEvent) {
    const el = (e.target as HTMLElement).closest("[data-call-id]") as HTMLElement | null;
    if (!el) return;
    const callId = el.getAttribute("data-call-id") as CallID;
    const kind = el.getAttribute("data-call-kind");
    if (kind !== "direct") return;

    e.stopPropagation(); // don't toggle our own ancestor's call sites

    if (expanded.has(callId)) {
      setExpanded((m) => {
        const n = new Map(m);
        n.delete(callId);
        return n;
      });
      return;
    }
    if (loading.has(callId)) return;
    setLoading((s) => new Set(s).add(callId));
    setErrors((m) => {
      const n = new Map(m);
      n.delete(callId);
      return n;
    });
    fetchBodyByCall(callId)
      .then((child) => {
        setLoading((s) => {
          const n = new Set(s);
          n.delete(callId);
          return n;
        });
        setExpanded((m) => new Map(m).set(callId, child));
      })
      .catch((err: Error) => {
        setLoading((s) => {
          const n = new Set(s);
          n.delete(callId);
          return n;
        });
        setErrors((m) => new Map(m).set(callId, err.message));
      });
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
      {/* Portals: render expanded child Frames inside each call's span. */}
      {Array.from(expanded.entries()).map(([cid, child]) => {
        const target = callElements.get(cid);
        if (!target) return null;
        return createPortal(
          <Frame
            key={cid}
            frame={child}
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

function prettyName(id: string): string {
  // Trim leading module path so headers fit. Keep last 2-3 segments.
  const parts = id.split("/");
  if (parts.length <= 2) return id;
  return ".../" + parts.slice(-2).join("/");
}

function shortPath(p: string): string {
  // Strip everything before the module root if we can find a hint;
  // otherwise show just the basename + parent.
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

// Minimal CSS.escape polyfill — Shiki's call IDs include ":" and "/".
function cssEscape(s: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") return CSS.escape(s);
  return s.replace(/([!"#$%&'()*+,./:;<=>?@[\\\]^`{|}~])/g, "\\$1");
}
