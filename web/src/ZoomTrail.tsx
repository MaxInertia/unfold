import type { ZoomLevel } from "./zoom";

// The trail across zoom levels. It looks like a breadcrumb but doesn't behave
// like one: clicking an ancestor does NOT truncate the tail.
//
// The reason is that the trailing entries *are* the anchor. The service level
// is only useful because it highlights the routes reaching `validateCoupon`;
// drop that entry and you've deleted what makes the level you just zoomed to
// meaningful. So every entry stays, the active level is marked, and entries
// below it render ghosted — you came from there, and clicking one returns you
// exactly, because nothing was destroyed.
export function ZoomTrail({
  level,
  serviceName,
  anchorTitle,
  entrypointCount,
  onZoom,
}: {
  level: ZoomLevel;
  serviceName: string | null;
  anchorTitle: string | null;
  // How many entrypoints reach the anchor. The route slot between service and
  // frame has no single answer until one is picked, so it reads "3 entrypoints"
  // rather than guessing one of them.
  entrypointCount: number | null;
  onZoom: (level: ZoomLevel) => void;
}) {
  if (!serviceName) return null;

  const atService = level === "service";

  return (
    <nav className="zoom-trail" aria-label="zoom level">
      <button
        type="button"
        className={`zoom-crumb${atService ? " zoom-crumb--active" : ""}`}
        onClick={() => onZoom("service")}
        title="zoom out to the service — its inbound surface and outbound dependencies"
      >
        {serviceName}
      </button>

      {anchorTitle && (
        <>
          <span className="zoom-sep">›</span>
          {/* The unfilled route slot: which entrypoint you came in through
              isn't decided until you pick one at the service level. */}
          <span
            className={`zoom-crumb zoom-crumb--slot${atService ? "" : " zoom-crumb--ghost"}`}
            title="the entrypoints that reach this frame — pick one at the service level to fill this slot"
          >
            {entrypointCount === null
              ? "⋯"
              : `${entrypointCount} entrypoint${entrypointCount === 1 ? "" : "s"}`}
          </span>
          <span className="zoom-sep">›</span>
          <button
            type="button"
            className={`zoom-crumb${atService ? " zoom-crumb--ghost" : " zoom-crumb--active"}`}
            onClick={() => onZoom("frame")}
            title="back to the code — your expansion state is untouched"
          >
            {anchorTitle}
          </button>
        </>
      )}
    </nav>
  );
}
