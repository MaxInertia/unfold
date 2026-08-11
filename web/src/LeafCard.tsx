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
// ends. That is one boundary with a choice, not three boundaries, so the card
// carries a picker and the buttons act on what is picked. Only the chosen end
// is resolved: the names are free (they come from the join), where the code at
// each end costs that service's index.
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
  // Which far end the buttons act on. A boundary with several ends is one
  // boundary with a choice, not several boundaries — the same shape the impl
  // switcher uses for a call that may dispatch to any of a set.
  const [picked, setPicked] = useState(0);

  const names = leaf.ends ?? [];
  const several = names.length > 1;
  // Nothing on the other side — or nothing indexed yet, which from here looks
  // the same and is the more honest of the two claims. Offering open and
  // inline would be offering to go somewhere that isn't there: they used to be
  // there, and answered with a 404 from the resolve behind them.
  const nowhere = names.length === 0;
  // Named, but with no code to point at: the service is an end of this key and
  // its handler isn't an indexed function. Worth saying once on the bar rather
  // than as the result of pressing a button that couldn't work.
  const unopenable = pickedEndHasNoCode(ends, names, picked);
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

  // The ends are named up front but their code isn't loaded until one is
  // chosen: resolving indexes the service being resolved into, which is a cost
  // nobody should pay for the ends they didn't pick.
  async function withPicked(what: string, run: (end: Endpoint) => Promise<void> | void) {
    setBusy(what);
    setError(null);
    try {
      const found = ends ?? (await far());
      setEnds(found);
      const end =
        found.find((e) => e.service === names[picked]) ?? found[Math.min(picked, found.length - 1)];
      if (end) await run(end);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  async function inlineEnd(end: Endpoint) {
    if (!end.target) {
      setError(`${end.service} is an end of this key but has no code to open`);
      return;
    }
    setInlined(await fetchBodyByTarget(end.target));
    onInline();
  }

  // Whatever we know about the chosen end — only after something resolved it.
  const pickedEnd = ends?.find((e) => e.service === names[picked]) ?? null;

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
        {/* One boundary with a choice, not several boundaries. Picking here
            costs nothing: the names came from the join, and only the end you
            act on is resolved. */}
        {several && (
          <select
            className="leaf-pick"
            value={picked}
            onChange={(e) => {
              setPicked(Number(e.target.value));
              // The spliced body belongs to the end that was showing.
              if (open) hide();
            }}
            aria-label="which service"
            title={`${names.length} services on the other side of ${leaf.key ?? "this key"}`}
          >
            {names.map((name, i) => (
              <option key={name} value={i}>
                {name}
              </option>
            ))}
          </select>
        )}
        {leaf.crossRepo && !nowhere && (
          <>
            <button
              type="button"
              className="leaf-action"
              disabled={!!busy}
              onClick={() =>
                void withPicked("open", (end) => {
                  if (end.target) onOpen(end.target);
                })
              }
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
                onClick={() => void withPicked("inline", inlineEnd)}
              >
                {busy === "inline" ? "indexing…" : "inline"}
              </button>
            )}
          </>
        )}
        {several && <span className="leaf-count">{names.length} services</span>}
        {nowhere && (
          <span
            className="leaf-nowhere"
            title={`nothing indexed in this workspace is on the other side of ${leaf.kind ?? "this key"} ${leaf.key ?? ""} — an end is only known once its service is indexed`}
          >
            {leaf.role === "inbound" ? "no indexed emitter" : "no indexed receiver"}
          </span>
        )}
        {unopenable && (
          <span className="leaf-nowhere" title="this service is an end of the key, but unfold could not identify the code at it">
            no code to open
          </span>
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
        {several && pickedEnd?.path && <span className="leaf-dest">{pickedEnd.path}</span>}
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

// pickedEndHasNoCode reports whether the end currently chosen resolved to a
// service with nothing openable in it. Only knowable after a resolve, which is
// why it is a state of the card rather than of the leaf.
function pickedEndHasNoCode(
  ends: Endpoint[] | null,
  names: string[],
  picked: number,
): boolean {
  if (!ends) return false;
  const end = ends.find((e) => e.service === names[picked]) ?? ends[0];
  return !!end && !end.target;
}
