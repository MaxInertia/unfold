import { useEffect, useState } from "react";
import { fetchBodyByTarget, resolveBinding } from "./api";
import type { Endpoint, Frame as FrameT, LeafInfo, TargetID } from "./types";

// The boundary a rule marked, rendered where the call site is.
//
// A client fronting another service shouldn't expand into transport plumbing —
// that's a body about marshalling, not about what happens next. What you
// actually want is the code on the other side, so the leaf offers it two ways:
// open it as a new root, or splice it in where the call is, the way an
// ordinary call expands.
//
// "The other side" is a direction, not a place. An emit leads to the services
// that subscribe; a subscription leads to the services that publish. And there
// can be several of either — a topic with three subscribers has three far
// ends, and picking one to show would be saying the other two don't receive
// it. So the card lists what it finds rather than resolving to a winner.
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
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [ends, setEnds] = useState<Endpoint[] | null>(null);

  // What the far side is called, from this side. The label is the rule
  // author's, so it already says it; this is for the list beneath.
  const several = (leaf.ends ?? 0) > 1;
  // Where the boundary leads when there is exactly one end — the server fills
  // this in only then, and only when that service is already indexed.
  const destination = leaf.targetPath || leaf.service || "";

  async function far(): Promise<Endpoint[]> {
    const res = await resolveBinding(leaf.kind ?? "grpc.method", leaf.key ?? "", leaf.role);
    if (res.ends?.length) return res.ends;
    // An engine that predates Ends still answers with the single-end fields.
    if (res.target) {
      return [
        {
          repo: res.repo,
          service: res.service,
          role: leaf.role === "inbound" ? "outbound" : "inbound",
          target: res.target,
          title: res.title,
          indexed: true,
        },
      ];
    }
    if (res.candidates?.length) {
      return [
        {
          repo: res.repo,
          service: res.service,
          role: leaf.role === "inbound" ? "outbound" : "inbound",
          target: res.candidates[0].targetId,
          title: res.candidates[0].label,
          indexed: true,
        },
      ];
    }
    setError(res.note ?? `${res.service ?? "the other service"} has no linkable implementation`);
    return [];
  }

  async function withEnds<T>(what: string, run: (ends: Endpoint[]) => Promise<T>) {
    setBusy(what);
    setError(null);
    try {
      const found = ends ?? (await far());
      setEnds(found);
      if (found.length) await run(found);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  function openEnd(end: Endpoint) {
    if (end.target) onOpen(end.target);
  }

  async function inlineEnd(end: Endpoint) {
    if (!end.target) return;
    setBusy(`inline:${end.repo}`);
    setError(null);
    try {
      setInlined(await fetchBodyByTarget(end.target));
      onInline();
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
        const found = await far();
        if (!alive || !found.length || !found[0].target) return;
        const body = await fetchBodyByTarget(found[0].target);
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
        {leaf.crossRepo && !several && (
          <>
            <button
              type="button"
              className="leaf-action"
              disabled={!!busy}
              onClick={() => void withEnds("open", async (e) => openEnd(e[0]))}
            >
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
                onClick={() => void withEnds("inline", (e) => inlineEnd(e[0]))}
              >
                {busy ? "indexing…" : "inline"}
              </button>
            )}
          </>
        )}
        {/* With several ends the buttons move onto the rows: there is no "the"
            far side to act on, and a card that acted on one of them would be
            answering a question nobody asked. */}
        {leaf.crossRepo && several && !ends && (
          <button
            type="button"
            className="leaf-action"
            disabled={!!busy}
            onClick={() => void withEnds("list", async () => {})}
          >
            {busy ? "indexing…" : `${leaf.ends} services`}
          </button>
        )}
        {/* One thing at the end: where this lands. The key isn't repeated —
            it's in the code the bar is sitting under. */}
        {!several && destination && (
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
      {ends && ends.length > 1 && (
        <ul className="leaf-ends">
          {ends.map((end) => (
            <li key={end.repo} className="leaf-end">
              <span className="leaf-end-service">{end.service}</span>
              <span className="leaf-end-title">{end.title || "(not linkable)"}</span>
              {end.target && (
                <>
                  <button type="button" className="leaf-action" onClick={() => openEnd(end)}>
                    open
                  </button>
                  <button
                    type="button"
                    className="leaf-action"
                    disabled={!!busy}
                    onClick={() => void inlineEnd(end)}
                  >
                    {busy === `inline:${end.repo}` ? "…" : "inline"}
                  </button>
                </>
              )}
              {end.path && <span className="leaf-dest">{end.path}</span>}
            </li>
          ))}
        </ul>
      )}
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
