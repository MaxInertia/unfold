import { useState } from "react";
import { fetchBodyByTarget, resolveBinding } from "./api";
import type { Endpoint, Frame as FrameT, LeafInfo, TargetID } from "./types";

// Picking the far side of a boundary.
//
// A client fronting another service shouldn't expand into transport plumbing —
// that's a body about marshalling, not about what happens next. What you want
// is the code on the other side, and "the other side" is a direction rather
// than a place: an emit leads to the services that subscribe, a subscription
// to the services that publish, and there can be several of either.
//
// Shaped like the callers panel, which answers the same kind of question one
// level down: something asks, the candidates appear, picking one changes the
// view and the list goes away. What it replaces stayed on screen carrying a
// dropdown and two buttons — a control panel parked in the middle of a
// function, still there long after the choice it offered had been made.
//
// The services are listed from the join, which costs nothing. Only the one
// picked is resolved, and resolving is what indexes the service at that end.
export function BoundaryPicker({
  leaf,
  onInline,
  onOpenRoot,
  onClose,
}: {
  leaf: LeafInfo;
  // Splice the far side in where the call is; the picker closes behind it.
  // The index says *which* end was picked, so the view can restore the same one
  // after a remount rather than defaulting back to the first.
  onInline: (frame: FrameT, endIndex: number) => void;
  // Or make it the root, the way picking a caller re-roots the view.
  onOpenRoot: (id: TargetID) => void;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const ends = leaf.ends ?? [];

  // Matched by position, not by service: one service can be an end more than
  // once — two subscriptions to the same event, in the same file — and picking
  // by name would collapse them onto whichever came first.
  async function endFor(index: number): Promise<Endpoint | null> {
    const res = await resolveBinding(leaf.kind ?? "grpc.method", leaf.key ?? "", leaf.role);
    const resolved = res.ends ?? [];
    const end = resolved[index] ?? resolved.find((e) => e.service === ends[index]?.service) ?? resolved[0] ?? null;
    if (end) return end;
    // An engine that predates Ends still answers with the single-end fields.
    if (res.target) {
      return {
        repo: res.repo,
        service: res.service,
        role: leaf.role === "inbound" ? "outbound" : "inbound",
        target: res.target,
        title: res.title,
        indexed: true,
      };
    }
    return null;
  }

  async function act(index: number, how: "inline" | "open") {
    const service = ends[index]?.service ?? "";
    setBusy(`${how}:${index}`);
    setError(null);
    try {
      const end = await endFor(index);
      if (!end?.target) {
        setError(
          `${service} is on the other side of this key, but unfold could not identify the code`,
        );
        return;
      }
      if (how === "open") {
        onOpenRoot(end.target);
        return;
      }
      onInline(await fetchBodyByTarget(end.target), index);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="boundary">
      <div className="boundary-head">
        <span className="boundary-title">
          {leaf.label || leaf.key || "boundary"}
          {ends.length > 1 && ` · ${ends.length}`}
        </span>
        <span className="boundary-hint">
          {ends.length > 1 ? "pick one to splice it in" : "splice it in where the call is"}
        </span>
        <button type="button" className="boundary-close" onClick={onClose} aria-label="close">
          ✕
        </button>
      </div>
      {error && <div className="boundary-note boundary-note--error">{error}</div>}
      <ul className="boundary-list">
        {ends.map((end, index) => (
          // Keyed by position: a service can appear more than once, so its
          // name is not an identity here.
          <li key={`${end.service}:${end.title ?? ""}:${index}`} className="boundary-end">
            <button
              type="button"
              className="boundary-pick"
              disabled={!!busy}
              onClick={() => void act(index, "inline")}
              title={`splice ${end.service}'s side of ${leaf.key ?? "this key"} in here`}
            >
              <span className="boundary-service">{end.service}</span>
              {/* The code at the end, for the services already read. Filling
                  this for the rest would mean indexing every service on the
                  other side just to draw a list of them. */}
              {end.title && <span className="boundary-target">{end.title}</span>}
              {/* Said, not glossed over: this is the function the handler is
                  registered in, because the handler itself is written inline
                  and has no name to point at. */}
              {end.viaSite && (
                <span
                  className="boundary-loc"
                  title="the handler is written inline at the registration, so this opens the function containing it"
                >
                  inline handler
                </span>
              )}
              {end.path && <span className="boundary-loc">{end.path}</span>}
              {!end.indexed && (
                <span className="boundary-loc" title="this service hasn't been indexed yet — picking it will read it first">
                  not indexed
                </span>
              )}
              {busy === `inline:${index}` && <span className="boundary-loc">indexing…</span>}
            </button>
            <button
              type="button"
              className="boundary-alt"
              disabled={!!busy}
              onClick={() => void act(index, "open")}
              title="open it as the root frame instead"
            >
              {busy === `open:${index}` ? "…" : "open"}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
