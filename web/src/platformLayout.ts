import {
  layoutGraph,
  linkLabelPoint,
  linkPath,
  type GraphLink,
  type GraphNode,
  type PlacedLink,
  type PlacedNode,
  type Point,
} from "./graphLayout";
import type { PlatformEdge, PlatformService } from "./types";

// The platform graph's adapter onto the shared layered layout.
//
// The layering, cycle-breaking and edge routing used to live here. They moved
// to graphLayout.ts when the call graph needed the same treatment — the cycle
// handling took two attempts to get right and a second copy would have drifted
// from this one within a week. What is left is the part that is genuinely about
// services: an edge's weight is how many RPCs it carries, so the loop of a
// cycle is closed by the lightest call rather than the busiest one.

export type { Point } from "./graphLayout";

export interface LaidOutNode {
  service: PlatformService;
  layer: number;
  index: number;
  x: number;
  y: number;
}

export interface LaidOutEdge {
  edge: PlatformEdge;
  from: LaidOutNode;
  to: LaidOutNode;
  back: boolean;
  points: Point[];
}

export interface Layout {
  nodes: LaidOutNode[];
  edges: LaidOutEdge[];
  width: number;
  height: number;
  layers: number;
}

// Sized for reading, not for density. The earlier values were tuned to fit a
// deep chain across the width of a card, which spent the one axis that was
// never scarce: a workspace is a handful of layers wide and the viewport has
// far more vertical room than the graph was using.
export const NODE_W = 168;
export const NODE_H = 52;

function edgeKey(e: PlatformEdge): string {
  return `${e.from}->${e.to}:${e.kind}`;
}

export function layout(
  services: PlatformService[],
  edges: PlatformEdge[],
  minHeight = 0,
): Layout {
  const nodes: GraphNode[] = services.map((s) => ({ id: s.alias, label: s.name }));
  const links: GraphLink[] = edges.map((e) => ({
    key: edgeKey(e),
    from: e.from,
    to: e.to,
    weight: e.calls.length,
  }));

  const g = layoutGraph(nodes, links, { nodeW: NODE_W, nodeH: NODE_H, minHeight });

  const byAlias = new Map<string, LaidOutNode>();
  const laidNodes: LaidOutNode[] = [];
  const svcOf = new Map(services.map((s) => [s.alias, s]));
  for (const p of g.nodes) {
    const service = svcOf.get(p.id);
    if (!service) continue;
    const n: LaidOutNode = { service, layer: p.layer, index: p.index, x: p.x, y: p.y };
    laidNodes.push(n);
    byAlias.set(p.id, n);
  }

  const placedByKey = new Map<string, PlacedLink>(g.links.map((l) => [l.key, l]));
  const laidEdges: LaidOutEdge[] = [];
  for (const e of edges) {
    const placed = placedByKey.get(edgeKey(e));
    const from = byAlias.get(e.from);
    const to = byAlias.get(e.to);
    if (!placed || !from || !to || from === to) continue;
    laidEdges.push({ edge: e, from, to, back: placed.back, points: placed.points });
  }

  return {
    nodes: laidNodes,
    edges: laidEdges,
    width: g.width,
    height: g.height,
    layers: g.layers,
  };
}

// The rendering helpers take the placed shape the shared layout produces, so
// the platform view's edges and the call graph's are drawn by one routine.
function asPlaced(e: LaidOutEdge): PlacedLink {
  const box = (n: LaidOutNode): PlacedNode => ({
    id: n.service.alias,
    layer: n.layer,
    index: n.index,
    x: n.x,
    y: n.y,
    w: NODE_W,
    h: NODE_H,
  });
  return { key: edgeKey(e.edge), from: box(e.from), to: box(e.to), back: e.back, points: e.points };
}

export function edgePath(e: LaidOutEdge): string {
  return linkPath(asPlaced(e));
}

export function labelPoint(e: LaidOutEdge): Point {
  return linkLabelPoint(asPlaced(e));
}
