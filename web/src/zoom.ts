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
export type ZoomLevel = "service" | "frame";

// Zooming out never destroys the view you came from — the frame tree and its
// expansion state live in the view store, untouched — so zooming back in is
// lossless and needs no snapshotting.
export function zoomOut(level: ZoomLevel): ZoomLevel {
  return level === "frame" ? "service" : level;
}

export function zoomIn(level: ZoomLevel): ZoomLevel {
  return level === "service" ? "frame" : level;
}
