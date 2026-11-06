import { useEffect, useRef, useState } from "react";
import { fetchBodyByCall, fetchBodyByTarget } from "./api";
import { blastFor, entryLabel, pathExpand, useDiffTour } from "./diffTour";
import { buildTree } from "./tourResolve";
import type { DiffRoute, Frame as FrameT } from "./types";
import { emptySlice, useViewStore } from "./viewState";

// The blast radius of a changed function: how many entrypoints reach it, on
// the frame header next to the diff badge. Click to see which, and pick one
// to re-root the view on it with the call path down to this frame opened —
// the change read from where execution would come in.
//
// Quiet by design: "from N", no colour, and at zero just a muted "from 0" with the
// reason in the tooltip. The header already says "modified"; this says who
// would notice.
export function BlastBadge({ frame }: { frame: FrameT }) {
  const { data } = useDiffTour();
  const fn = blastFor(data, frame.id);
  const [open, setOpen] = useState(false);
  const store = useViewStore();
  const ref = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!fn || !data) return null;
  const n = fn.entrypoints.length;
  const known = data.entrypointsKnown;

  async function pick(route: DiffRoute) {
    setOpen(false);
    // Root on the entrypoint; open the hops down to this frame the way a
    // tour's expand would, so the path is on screen rather than implied.
    const expand = pathExpand(route.path, data!.names);
    let tree = emptySlice;
    if (expand) {
      try {
        const root = await fetchBodyByTarget(route.id);
        tree = (await buildTree(root, [expand], fetchBodyByCall)).tree;
      } catch {
        /* the entrypoint still opens; the path just isn't pre-expanded */
      }
    }
    store.setView(route.id, tree);
  }

  const title = !known
    ? "this engine can't name where execution starts"
    : n === 0
      ? "not reached from any recognized entrypoint"
      : `reached from ${n} ${n === 1 ? "entrypoint" : "entrypoints"} — click to list them`;

  return (
    <span className="frame-blast" ref={ref}>
      <button
        type="button"
        className={`frame-blast-count${n === 0 ? " frame-blast-count--none" : ""}${open ? " frame-blast-count--open" : ""}`}
        onClick={() => n > 0 && setOpen((v) => !v)}
        disabled={n === 0}
        title={title}
        aria-label={title}
        aria-expanded={n > 0 ? open : undefined}
        data-blast={n}
      >
        from {known ? n : "?"}
      </button>
      {open && (
        <div className="frame-blast-pop" role="menu">
          <div className="frame-blast-head">
            reached from · pick one to open it as the root with the path down to here
          </div>
          <ul className="frame-blast-list">
            {fn.entrypoints.map((r, i) => (
              <li key={r.id}>
                <button
                  type="button"
                  className="frame-blast-entry"
                  role="menuitem"
                  onClick={() => void pick(r)}
                  title={r.path.map((id) => data.names[id] ?? id).join(" → ")}
                >
                  <span className="frame-blast-entry-title">{r.title}</span>
                  <span className="frame-blast-entry-kind">{entryLabel(r)}</span>
                  <span className="frame-blast-entry-hops">
                    {r.path.length === 1
                      ? i === 0
                        ? "this function"
                        : ""
                      : `${r.path.length - 1} ${r.path.length === 2 ? "hop" : "hops"}`}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </span>
  );
}

// The count alone, for the pinned copy of a header. Reads the same store, so
// the two never disagree.
export function useBlastCount(id: string): number | null {
  const { data } = useDiffTour();
  const fn = blastFor(data, id);
  return fn ? fn.entrypoints.length : null;
}
