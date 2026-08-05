// Zoom levels. The ladder is reachability, not containment: "which
// entrypoints reach this function" tells you why the code runs, where "which
// file is it in" tells you how someone split up text. The file is the unit
// unfold exists to dissolve, so it isn't a rung — a whole-file view stays a
// lateral move (Frame("file:<path>")), not a zoom step.
//
// The full ladder is service ← entrypoints ← frame, with a platform level
// above service once more than one repo is indexed. Today only the two ends
// exist; the entrypoint rung is present as data (which inbound bindings reach
// the anchor) but has no screen of its own yet.
export type ZoomLevel = "platform" | "service" | "frame";

// Ordered outermost-in. Zooming walks this list, skipping levels that aren't
// available (there's no platform above a single repo).
export const LEVELS: ZoomLevel[] = ["platform", "service", "frame"];

// Zooming out never destroys the view you came from — the frame tree and its
// expansion state live in the view store, untouched — so zooming back in is
// lossless and needs no snapshotting.
function step(level: ZoomLevel, delta: number, hasPlatform: boolean): ZoomLevel {
  const usable = hasPlatform ? LEVELS : LEVELS.filter((l) => l !== "platform");
  const i = usable.indexOf(level);
  if (i === -1) return level;
  return usable[Math.min(usable.length - 1, Math.max(0, i + delta))];
}

export function zoomOut(level: ZoomLevel, hasPlatform: boolean): ZoomLevel {
  return step(level, -1, hasPlatform);
}

export function zoomIn(level: ZoomLevel, hasPlatform: boolean): ZoomLevel {
  return step(level, 1, hasPlatform);
}
