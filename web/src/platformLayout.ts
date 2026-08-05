import type { PlatformEdge, PlatformService } from "./types";

// Layout for the platform graph.
//
// Deterministic, no physics: a force simulation on a service graph settles
// differently every load, which makes the picture unmemorable and impossible
// to talk about ("the one on the left" stops meaning anything). Layered
// assignment plus barycenter ordering is the standard alternative and it puts
// the dependency direction on the x axis, which is the thing you actually
// want to read — callers on the left, callees on the right.

export interface LaidOutNode {
  service: PlatformService;
  layer: number;
  index: number; // position within the layer
  x: number;
  y: number;
}

export interface LaidOutEdge {
  edge: PlatformEdge;
  from: LaidOutNode;
  to: LaidOutNode;
  // A back edge points against the layering — the graph has a cycle here, and
  // saying so is more useful than pretending the layering was clean.
  back: boolean;
}

export interface Layout {
  nodes: LaidOutNode[];
  edges: LaidOutEdge[];
  width: number;
  height: number;
  layers: number;
}

// Deliberately compact. Layering by longest path means a chain of six
// services is six columns wide, so per-column cost is what decides whether a
// real platform fits on screen at all.
export const NODE_W = 132;
export const NODE_H = 38;
const COL_GAP = 58;
const ROW_GAP = 14;
const PAD = 14;

// layerOf assigns each service a column: one past the deepest service that
// calls it. Cycles can't be layered, so a node already being resolved is
// treated as layer 0 for that path — the edge that closed the loop is then
// drawn as a back edge instead of silently reordering the graph.
function assignLayers(services: PlatformService[], edges: PlatformEdge[]): Map<string, number> {
  const callers = new Map<string, string[]>();
  for (const e of edges) {
    if (e.from === e.to) continue;
    callers.set(e.to, [...(callers.get(e.to) ?? []), e.from]);
  }
  const layer = new Map<string, number>();
  const visiting = new Set<string>();

  function depth(alias: string): number {
    const known = layer.get(alias);
    if (known !== undefined) return known;
    if (visiting.has(alias)) return 0; // cycle: stop descending
    visiting.add(alias);
    let d = 0;
    for (const c of callers.get(alias) ?? []) {
      d = Math.max(d, depth(c) + 1);
    }
    visiting.delete(alias);
    layer.set(alias, d);
    return d;
  }

  for (const s of services) depth(s.alias);
  return layer;
}

// order sorts each layer to reduce edge crossings: repeatedly place a node at
// the average position of its neighbours in the previous layer. Two passes is
// plenty at this scale and keeps the result stable.
function orderLayers(
  byLayer: PlatformService[][],
  edges: PlatformEdge[],
  layer: Map<string, number>,
): void {
  const pos = new Map<string, number>();
  const setPos = () => byLayer.forEach((l) => l.forEach((s, i) => pos.set(s.alias, i)));
  setPos();

  const peersFromLeft = new Map<string, string[]>();
  for (const e of edges) {
    if ((layer.get(e.from) ?? 0) < (layer.get(e.to) ?? 0)) {
      peersFromLeft.set(e.to, [...(peersFromLeft.get(e.to) ?? []), e.from]);
    }
  }

  for (let pass = 0; pass < 2; pass++) {
    for (let l = 1; l < byLayer.length; l++) {
      byLayer[l].sort((a, b) => barycenter(a) - barycenter(b) || a.name.localeCompare(b.name));
    }
    setPos();
  }

  function barycenter(s: PlatformService): number {
    const peers = peersFromLeft.get(s.alias) ?? [];
    if (peers.length === 0) return Number.MAX_SAFE_INTEGER; // unconnected sink to the end
    return peers.reduce((sum, p) => sum + (pos.get(p) ?? 0), 0) / peers.length;
  }
}

export function layout(services: PlatformService[], edges: PlatformEdge[]): Layout {
  if (services.length === 0) {
    return { nodes: [], edges: [], width: 0, height: 0, layers: 0 };
  }
  const present = new Set(services.map((s) => s.alias));
  const visible = edges.filter((e) => present.has(e.from) && present.has(e.to));

  const layerOf = assignLayers(services, visible);
  const maxLayer = Math.max(...services.map((s) => layerOf.get(s.alias) ?? 0));
  const byLayer: PlatformService[][] = Array.from({ length: maxLayer + 1 }, () => []);
  for (const s of [...services].sort((a, b) => a.name.localeCompare(b.name))) {
    byLayer[layerOf.get(s.alias) ?? 0].push(s);
  }
  orderLayers(byLayer, visible, layerOf);

  const nodes: LaidOutNode[] = [];
  const byAlias = new Map<string, LaidOutNode>();
  const tallest = Math.max(...byLayer.map((l) => l.length));
  byLayer.forEach((list, l) => {
    // Centre short layers against the tallest one so the graph reads as a
    // shape rather than everything jammed to the top.
    const offset = ((tallest - list.length) * (NODE_H + ROW_GAP)) / 2;
    list.forEach((s, i) => {
      const n: LaidOutNode = {
        service: s,
        layer: l,
        index: i,
        x: PAD + l * (NODE_W + COL_GAP),
        y: PAD + offset + i * (NODE_H + ROW_GAP),
      };
      nodes.push(n);
      byAlias.set(s.alias, n);
    });
  });

  const laidEdges: LaidOutEdge[] = [];
  for (const e of visible) {
    const from = byAlias.get(e.from);
    const to = byAlias.get(e.to);
    if (!from || !to || from === to) continue;
    laidEdges.push({ edge: e, from, to, back: to.layer <= from.layer });
  }

  return {
    nodes,
    edges: laidEdges,
    width: PAD * 2 + (maxLayer + 1) * NODE_W + maxLayer * COL_GAP,
    height: PAD * 2 + tallest * NODE_H + Math.max(0, tallest - 1) * ROW_GAP,
    layers: maxLayer + 1,
  };
}

// edgePath draws a cubic between two nodes. Back edges bow outward so they're
// distinguishable from the forward flow rather than overlapping it.
export function edgePath(e: LaidOutEdge): string {
  const x1 = e.from.x + NODE_W;
  const y1 = e.from.y + NODE_H / 2;
  const x2 = e.to.x;
  const y2 = e.to.y + NODE_H / 2;
  if (e.back) {
    const bow = 26 + Math.abs(e.from.layer - e.to.layer) * 8;
    const midY = Math.min(y1, y2) - bow;
    return `M ${e.from.x} ${y1} C ${e.from.x - bow} ${midY}, ${x2 + NODE_W + bow} ${midY}, ${x2 + NODE_W} ${y2}`;
  }
  const dx = Math.max(30, (x2 - x1) / 2);
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}
