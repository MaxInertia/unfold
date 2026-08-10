import { useEffect, useState } from "react";
import { fetchBodyByTarget, resolveBinding } from "./api";
import type { Frame as FrameT, LeafInfo, TargetID } from "./types";

// The boundary a rule marked, rendered where the call site is.
//
// A client fronting another service shouldn't expand into transport plumbing —
// that's a body about marshalling, not about what happens next. What you
// actually want is the handler on the other side, so the leaf offers it two
// ways: open it as a new root, or splice it in where the call is, the way an
// ordinary call expands.
//
// Inlining across a network hop makes the trace read as one continuous body
// when execution actually left the process, so the boundary stays visible even
// when the far side is shown. Removing it would make the caller look like it
// calls the remote handler directly, which is the same class of lie the tool
// works to avoid elsewhere.
export function LeafCard({
  leaf,
  open,
  onInline,
  onHide,
  onOpen,
  renderFrame,
}: {
  leaf: LeafInfo;
  // Whether the spliced body is showing. Owned by the slice rather than by
  // this component: the body is a subtree of the view, so "collapse all", a
  // shared URL and the back button all have to be able to speak about it.
  open: boolean;
  onInline: () => void;
  onHide: () => void;
  onOpen: (id: TargetID) => void;
  // Renders a resolved remote frame inline. Supplied by Frame so the spliced
  // body gets the same expansion machinery as any other child.
  renderFrame: (frame: FrameT) => React.ReactNode;
}) {
  const [inlined, setInlined] = useState<FrameT | null>(null);
  const [busy, setBusy] = useState<"open" | "inline" | null>(null);
  const [error, setError] = useState<string | null>(null);

  // What the boundary leads to. The path is there when the far service is
  // already indexed; naming the service alone is the honest fallback, because
  // finding the function would mean indexing that repo just to draw this bar.
  const destination = leaf.targetPath || leaf.service || "";

  async function far(): Promise<TargetID | null> {
    const res = await resolveBinding(leaf.kind ?? "grpc.method", leaf.key ?? "");
    if (res.target) return res.target;
    if (res.candidates?.length) return res.candidates[0].targetId;
    setError(res.note ?? `${res.service ?? "the other service"} has no linkable implementation`);
    return null;
  }

  async function openAsRoot() {
    setBusy("open");
    setError(null);
    try {
      const t = await far();
      if (t) onOpen(t);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  async function inline() {
    setBusy("inline");
    setError(null);
    try {
      const t = await far();
      if (t) {
        setInlined(await fetchBodyByTarget(t));
        onInline();
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  function hide() {
    setInlined(null);
    onHide();
  }

  // A slot that's open with no body is a view restored from a URL or from the
  // back button — the slice remembers that the far side was spliced in, and the
  // body itself was never in it. Resolve it again rather than showing a card
  // that says nothing is there.
  useEffect(() => {
    if (!open || inlined || busy) return;
    let alive = true;
    void (async () => {
      try {
        const t = await far();
        if (!t || !alive) return;
        const body = await fetchBodyByTarget(t);
        if (alive) setInlined(body);
      } catch (e) {
        if (alive) setError((e as Error).message);
      }
    })();
    return () => {
      alive = false;
    };
    // far() closes over the leaf, which is stable for a given call site.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, inlined, busy]);

  return (
    <div className="leaf-card">
      <div className="leaf-bar">
        <span className="leaf-label">{leaf.label || leaf.key || "boundary"}</span>
        {leaf.crossRepo && (
          <>
            <button type="button" className="leaf-action" disabled={!!busy} onClick={() => void openAsRoot()}>
              {busy === "open" ? "…" : "open"}
            </button>
            {open ? (
              <button type="button" className="leaf-action" onClick={hide}>
                hide
              </button>
            ) : (
              <button
                type="button"
                className="leaf-action"
                disabled={!!busy}
                onClick={() => void inline()}
              >
                {busy === "inline" ? "indexing…" : "inline"}
              </button>
            )}
          </>
        )}
        {/* One thing at the end: where this lands. The key isn't repeated —
            it's in the code the bar is sitting under, and a bar that wrapped to
            two lines cost more than it explained. Everything else it used to
            say is a tooltip away. */}
        {destination && (
          <span
            className="leaf-dest"
            title={[
              leaf.targetTitle && `${leaf.targetTitle} in ${leaf.service}`,
              leaf.key && `${leaf.kind ?? "key"} ${leaf.key}`,
              `recognized by rule ${leaf.rule}`,
            ]
              .filter(Boolean)
              .join(" — ")}
          >
            {destination}
          </span>
        )}
      </div>
      {error && <div className="call-error">{error}</div>}
      {open && inlined && (
        // inline-child as well, so the spliced body is laid out like every
        // other inline expansion — classic indent mode offsets that class, and
        // a body that stayed flush left was the one child of a frame that
        // didn't follow the setting.
        <div className="leaf-inlined inline-child">
          {/* The hop stays named even with the far side spliced in. */}
          <div className="leaf-hop">execution leaves this process here</div>
          {renderFrame(inlined)}
        </div>
      )}
    </div>
  );
}
