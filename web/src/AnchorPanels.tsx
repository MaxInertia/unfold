import { BindingRow } from "./BindingRow";
import type { ServiceView, TargetID } from "./types";

// The L1.5 rung, finally given a surface — but as panels flanking the code
// rather than a screen of its own.
//
// The vault's argument for L1.5 was that deep inside `validateCoupon`, "it's
// in coupon.go with 8 other functions" is worthless while "it's reached by
// POST /v1/orders and the nightly-reprice subscription" is the orienting
// fact. That's true, and it's also true that you want the fact *while reading
// the code*, not after zooming away from it. So the same data the service
// level renders in columns renders here beside the frame: entrypoints that
// reach the anchor on the left, calls the anchor makes on the right.
//
// Nothing new is fetched. Both lists are the /api/service response filtered
// by the reachability flags the backend already computes — the same walk that
// lights rows at L1, shown at L2.

// Why a panel is empty is more useful than the fact that it is, and the
// reasons are genuinely different: no service data at all, no anchor to walk
// from, or a walk that ran and found nothing.
function emptyReason(
  view: ServiceView | null,
  anchored: boolean,
  kind: "entrypoints" | "outbounds",
): string {
  if (!view) return "No service view — this repo has no recognized surface yet.";
  if (!anchored) return "Open a function to anchor on it.";
  return kind === "entrypoints"
    ? "No recognized entrypoint reaches this frame. It may be reached only through helpers, or by a router unfold can't read yet."
    : "This frame's code path makes no recognized outbound call.";
}

export function EntrypointsPanel({
  view,
  onOpen,
}: {
  view: ServiceView | null;
  onOpen: (id: TargetID) => void;
}) {
  const anchored = !!view?.anchorTitle;
  const rows = view?.inbound.filter((b) => b.reachesAnchor) ?? [];

  return (
    <div className="anchor-panel">
      <p className="anchor-panel-hint">
        {anchored ? (
          <>
            entrypoints that reach <b>{view!.anchorTitle}</b>
          </>
        ) : (
          "entrypoints that reach the open frame"
        )}
      </p>
      {rows.length === 0 ? (
        <p className="tree-placeholder">{emptyReason(view, anchored, "entrypoints")}</p>
      ) : (
        <ul className="service-list anchor-panel-list">
          {rows.map((b, i) => (
            // anchored={false} because every row here already reaches the
            // anchor — that's the filter. Dimming would grey out nothing and
            // lighting would light everything, so the treatment says nothing
            // and the badge would just repeat the panel's own title.
            <BindingRow
              key={`${b.kind}:${b.key}:${b.file}:${b.line}:${i}`}
              binding={{ ...b, reachesAnchor: undefined }}
              anchored={false}
              onOpen={onOpen}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

export function OutboundsPanel({
  view,
  onOpen,
}: {
  view: ServiceView | null;
  onOpen: (id: TargetID) => void;
}) {
  const anchored = !!view?.anchorTitle;
  const rows = view?.outbound.filter((b) => b.reachedByAnchor) ?? [];

  return (
    <div className="anchor-panel">
      <p className="anchor-panel-hint">
        {anchored ? (
          <>
            what <b>{view!.anchorTitle}</b> calls out to
          </>
        ) : (
          "what the open frame calls out to"
        )}
      </p>
      {rows.length === 0 ? (
        <p className="tree-placeholder">{emptyReason(view, anchored, "outbounds")}</p>
      ) : (
        <ul className="service-list anchor-panel-list">
          {rows.map((b, i) => (
            <BindingRow
              key={`${b.kind}:${b.key}:${b.file}:${b.line}:${i}`}
              binding={{ ...b, reachedByAnchor: undefined }}
              anchored={false}
              onOpen={onOpen}
            />
          ))}
        </ul>
      )}
    </div>
  );
}
