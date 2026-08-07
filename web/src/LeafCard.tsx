import { useState } from "react";
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
  onOpen,
  renderFrame,
}: {
  leaf: LeafInfo;
  onOpen: (id: TargetID) => void;
  // Renders a resolved remote frame inline. Supplied by Frame so the spliced
  // body gets the same expansion machinery as any other child.
  renderFrame: (frame: FrameT) => React.ReactNode;
}) {
  const [inlined, setInlined] = useState<FrameT | null>(null);
  const [busy, setBusy] = useState<"open" | "inline" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function far(): Promise<TargetID | null> {
    const res = await resolveBinding(leaf.kind ?? "grpc.method", leaf.key ?? "");
    if (res.target) return res.target;
    if (res.candidates?.length) return res.candidates[0].targetId;
    setError(res.note ?? `${res.service ?? "the other service"} has no linkable implementation`);
    return null;
  }

  async function open() {
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
      if (t) setInlined(await fetchBodyByTarget(t));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="leaf-card">
      <div className="leaf-bar">
        <span className="leaf-label">{leaf.label || leaf.key || "boundary"}</span>
        {leaf.key && <span className="leaf-key">{leaf.key}</span>}
        {leaf.crossRepo && (
          <>
            <button type="button" className="leaf-action" disabled={!!busy} onClick={() => void open()}>
              {busy === "open" ? "…" : "open"}
            </button>
            {inlined ? (
              <button type="button" className="leaf-action" onClick={() => setInlined(null)}>
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
        <span className="leaf-rule" title={`recognized by rule ${leaf.rule}`}>
          {leaf.rule}
        </span>
      </div>
      {error && <div className="call-error">{error}</div>}
      {inlined && (
        <div className="leaf-inlined">
          {/* The hop stays named even with the far side spliced in. */}
          <div className="leaf-hop">execution leaves this process here</div>
          {renderFrame(inlined)}
        </div>
      )}
    </div>
  );
}
