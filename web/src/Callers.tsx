import { useEffect, useState } from "react";
import { fetchUsages } from "./api";
import type { CallID, Frame as FrameT, Usage } from "./types";
import {
  emptySlice,
  useViewStore,
  type FramePath,
  type FrameSlice,
} from "./viewState";

// spliceAbove builds the slice tree for re-rooting on a caller: the given
// subtree (the clicked frame's current expansion state) nests inside the
// caller at the usage's call site, so the caller reads as spliced above.
// A value reference has no call site to splice through, so the caller
// opens bare.
export function spliceAbove(u: Usage, subtree: FrameSlice): FrameSlice {
  if (!u.callId) return emptySlice;
  return {
    folds: [],
    expansions: { [u.callId]: { ...subtree, choice: u.choice ?? 0 } },
  };
}

// CallersPanel lists every usage of a frame's symbol as an excerpt strip.
// Picking one re-roots the view: the caller becomes the root frame and the
// clicked frame's current subtree stays expanded at the picked call site.
export function CallersPanel({
  frame,
  path,
  onClose,
}: {
  frame: FrameT;
  path: FramePath;
  onClose: () => void;
}) {
  const store = useViewStore();
  const [usages, setUsages] = useState<Usage[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setUsages(null);
    setError(null);
    fetchUsages(frame.id)
      .then((u) => {
        if (alive) setUsages(u);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      });
    return () => {
      alive = false;
    };
  }, [frame.id]);

  // The call site this frame is currently expanded under — its caller is
  // already on screen as the parent frame.
  const parentCallId: CallID | null =
    path.length > 0 ? path[path.length - 1].callId : null;

  function pick(u: Usage) {
    store.setView(u.caller, spliceAbove(u, store.getSlice(path)));
    onClose();
  }

  return (
    <div className="callers">
      <div className="callers-head">
        <span className="callers-title">
          callers{usages ? ` · ${usages.length}` : ""}
        </span>
        <span className="callers-hint">pick one to splice it above</span>
        <button
          type="button"
          className="callers-close"
          onClick={onClose}
          aria-label="close callers"
        >
          ✕
        </button>
      </div>
      {error && <div className="callers-note callers-note--error">{error}</div>}
      {!error && usages === null && <div className="callers-note">loading…</div>}
      {usages && usages.length === 0 && (
        <div className="callers-note">no usages found in the indexed project</div>
      )}
      {usages && usages.length > 0 && (
        <ul className="callers-list">
          {usages.map((u, i) => (
            <UsageStrip
              key={`${u.file}:${u.line}:${i}`}
              usage={u}
              inView={!!u.callId && u.callId === parentCallId}
              onPick={() => pick(u)}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

// One usage as a clickable strip: caller name, kind badge, location, and
// the excerpt with the usage line highlighted.
function UsageStrip({
  usage,
  inView,
  onPick,
}: {
  usage: Usage;
  inView: boolean;
  onPick: () => void;
}) {
  const hotLine = usage.line - usage.excerptLine;
  const lines = usage.excerpt ? usage.excerpt.split("\n") : [];
  const isRef = usage.kind === "ref";
  return (
    <li className={`caller${isRef ? " caller--ref" : ""}`}>
      <button
        type="button"
        className="caller-strip"
        onClick={onPick}
        title={
          isRef
            ? "value reference, not a call — opens the caller bare (this frame can't be spliced into it)"
            : "make this caller the root; this frame stays expanded at the call site"
        }
      >
        <span className="caller-head">
          <span className="caller-name">{usage.callerTitle}</span>
          <span className={`caller-kind caller-kind--${usage.kind}`}>
            {kindLabel(usage.kind)}
          </span>
          {isRef && <span className="caller-noslice">⤳ not a call · opens bare</span>}
          {inView && <span className="caller-kind caller-kind--inview">in view</span>}
          <span className="caller-loc">
            {shortPath(usage.file)}:{usage.line}
          </span>
        </span>
        {lines.length > 0 && (
          <pre className="caller-excerpt">
            {lines.map((l, i) => (
              <span
                key={i}
                className={`caller-excerpt-line${
                  i === hotLine ? " caller-excerpt-line--hot" : ""
                }`}
              >
                {l || " "}
                {"\n"}
              </span>
            ))}
          </pre>
        )}
      </button>
    </li>
  );
}

export function kindLabel(kind: Usage["kind"]): string {
  if (kind === "interface") return "iface";
  return kind;
}

function shortPath(p: string): string {
  const parts = p.split("/");
  return parts.slice(-2).join("/");
}
