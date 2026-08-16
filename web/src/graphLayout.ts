// Layered graph layout, shared by every node-link view.
//
// Deterministic, no physics: a force simulation settles differently every load,
// which makes the picture unmemorable and impossible to talk about ("the one on
// the left" stops meaning anything). Layered assignment plus barycenter
// ordering is the standard alternative and it puts the dependency direction on
// the x axis, which is the thing you actually want to read — callers on the
// left, callees on the right.
//
// The layering is only half of it. An edge that spans more than one layer has
// to be *routed*, not drawn straight: with A→B→C and A→C, the A→C edge runs
// horizontally through B's column, arrives at C from the same direction B's
// edge does, and reads as "A stops at B". The graph is then lying about the
// dependency, which is the one thing these views exist to get right. So long
// edges are broken into per-layer waypoints that take their own slot in the
// ordering — the standard Sugiyama dummy-vertex step. A→C now visibly bends
// around B, and the ordering pass can no longer stack a node on top of an edge,
// because the edge occupies a row of its own.
//
// This file knows nothing about services or APIs. It was extracted from the
// platform view when the call graph needed the same treatment: the cycle
// handling and the routing took two attempts to get right, and a second copy
// would have drifted from the first the week after it was made.

export interface Point {
  x: number;
  y: number;
}

// GraphNode is the caller's node reduced to what layout needs.
export interface GraphNode {
  id: string;
  // label breaks ordering ties, so the same graph always lays out identically.
  // Ties broken on identity rather than on nothing is what keeps "which node
  // ended up above which" from wobbling between loads.
  label: string;
  // Height, for callers whose nodes aren't all the same size. Defaults to the
  // layout's nodeH.
  height?: number;
}

// GraphLink is a directed edge. weight decides which edge of a cycle bows
// backwards — see findBackEdges.
export interface GraphLink {
  key: string;
  from: string;
  to: string;
  weight: number;
}

export interface LayoutOptions {
  nodeW: number;
  nodeH: number;
  // Height the caller has room for. Purely an invitation to spread out: the
  // layout never shrinks below its natural size to satisfy it.
  minHeight?: number;
  colGap?: number;
  rowGap?: number;
  maxRowGap?: number;
}

export interface PlacedNode {
  id: string;
  layer: number;
  index: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface PlacedLink {
  key: string;
  from: PlacedNode;
  to: PlacedNode;
  // A back edge points against the layering — the graph has a cycle here, and
  // saying so is more useful than pretending the layering was clean.
  back: boolean;
  // The full route, endpoints included. Length > 2 means the edge skips at
  // least one layer and is being steered around it.
  points: Point[];
}

export interface GraphLayout {
  nodes: PlacedNode[];
  byId: Map<string, PlacedNode>;
  links: PlacedLink[];
  width: number;
  height: number;
  layers: number;
}

const COL_GAP = 96;
const ROW_GAP = 26;
// Vertical room a routed edge reserves as it crosses a layer. Thinner than a
// node — it only has to clear the nodes around it, and a full-height channel
// per skipped edge would push a wide graph off the screen.
const CHANNEL_H = 16;
const PAD = 20;
// A graph is often a handful of layers wide but only a few rows tall, so the
// natural height leaves most of a tall window empty while the rows sit packed
// together. Rather than pick a bigger constant — which would then overflow a
// genuinely tall graph — the spare height is handed back as row gap, up to a
// ceiling past which extra space stops buying legibility and starts making
// related nodes look unrelated.
const MAX_ROW_GAP = 88;

// A row in a layer: either a real node or a waypoint where some edge crosses
// this column. Ordering treats both the same, which is the whole trick — a
// routed edge competes for vertical position instead of being drawn wherever
// it lands.
interface Slot {
  id: string;
  real: boolean;
  label: string;
  layer: number;
  index: number;
  x: number;
  y: number;
  h: number;
}

// Which edges close a cycle, found by depth-first search: an edge into a node
// that is still on the stack points backwards.
//
// This has to run *before* layering, not during it. Layering a graph that still
// contains its cycles lets a single back edge inflate the depth of everything
// downstream of it — one ledger→orders edge pushes `orders` past `billing` and
// `ledger`, and the gateway→orders call then has to snake across the entire
// graph to reach a node that is actually one hop away. The picture stops
// matching the dependency it's drawing. Breaking cycles first costs one DFS and
// keeps every forward edge pointing forward.
//
// Any edge of a cycle can be the one declared "backwards", and the choice
// decides the whole shape, so it is made deliberately rather than by whatever
// order the DFS happened to arrive in:
//
//   - Roots are taken in-degree first, so the search starts at the nodes
//     nothing points to. Entering a cycle from its natural entry point breaks
//     the edge that closes the loop rather than one partway around it.
//   - Each node's edges are walked heaviest first, so the loop is closed by the
//     *lightest* edge. Between a call carrying twelve RPCs and one carrying a
//     single callback, the callback is the one that should bow backwards;
//     reversing the heavy edge would misdescribe the main flow.
//
// Ties fall back to key, so the same graph always lays out identically; "which
// edge got called the cycle" can't be the thing that wobbles between loads.
function findBackEdges(nodes: GraphNode[], links: GraphLink[]): Set<string> {
  const out = new Map<string, GraphLink[]>();
  const inDegree = new Map<string, number>();
  for (const n of nodes) inDegree.set(n.id, 0);
  for (const e of links) {
    if (e.from === e.to) continue;
    out.set(e.from, [...(out.get(e.from) ?? []), e]);
    inDegree.set(e.to, (inDegree.get(e.to) ?? 0) + 1);
  }
  for (const list of out.values()) {
    list.sort((a, b) => b.weight - a.weight || a.key.localeCompare(b.key));
  }

  const back = new Set<string>();
  // 0 = unseen, 1 = on the current stack, 2 = finished
  const state = new Map<string, 0 | 1 | 2>();
  const stack: string[] = [];

  const roots = nodes
    .map((n) => n.id)
    .sort((a, b) => (inDegree.get(a) ?? 0) - (inDegree.get(b) ?? 0) || a.localeCompare(b));

  // Iterative, so a deep graph can't blow the call stack.
  for (const start of roots) {
    if ((state.get(start) ?? 0) !== 0) continue;
    stack.push(start);
    const iter = new Map<string, number>();
    while (stack.length > 0) {
      const a = stack[stack.length - 1];
      if ((state.get(a) ?? 0) === 0) state.set(a, 1);
      const list = out.get(a) ?? [];
      const i = iter.get(a) ?? 0;
      if (i >= list.length) {
        state.set(a, 2);
        stack.pop();
        continue;
      }
      iter.set(a, i + 1);
      const e = list[i];
      const st = state.get(e.to) ?? 0;
      if (st === 1) back.add(e.key);
      else if (st === 0) stack.push(e.to);
    }
  }
  return back;
}

// assignLayers gives each node a column: one past the deepest node pointing at
// it. Only forward edges are passed in — cycles are broken first — so the
// recursion always terminates on a DAG.
function assignLayers(nodes: GraphNode[], links: GraphLink[]): Map<string, number> {
  const preds = new Map<string, string[]>();
  for (const e of links) {
    if (e.from === e.to) continue;
    preds.set(e.to, [...(preds.get(e.to) ?? []), e.from]);
  }
  const layer = new Map<string, number>();
  const visiting = new Set<string>();

  function depth(id: string): number {
    const known = layer.get(id);
    if (known !== undefined) return known;
    if (visiting.has(id)) return 0; // cycle: stop descending
    visiting.add(id);
    let d = 0;
    for (const c of preds.get(id) ?? []) d = Math.max(d, depth(c) + 1);
    visiting.delete(id);
    layer.set(id, d);
    return d;
  }

  for (const n of nodes) depth(n.id);
  return layer;
}

// orderLayers sorts each layer to reduce edge crossings: repeatedly place a
// slot at the average position of its predecessors in the previous layer.
// Waypoints take part, so a routed edge is pulled toward the rows it connects
// rather than cutting across the ones it doesn't.
function orderLayers(byLayer: Slot[][], preds: Map<string, string[]>): void {
  const pos = new Map<string, number>();
  const setPos = () => byLayer.forEach((l) => l.forEach((s, i) => pos.set(s.id, i)));
  setPos();

  const barycenter = (s: Slot): number => {
    const p = preds.get(s.id) ?? [];
    if (p.length === 0) return Number.MAX_SAFE_INTEGER; // unconnected sink to the end
    return p.reduce((sum, id) => sum + (pos.get(id) ?? 0), 0) / p.length;
  };

  // Three passes rather than two: waypoints add a rung to most chains, so the
  // ordering needs one more round to propagate all the way right.
  for (let pass = 0; pass < 3; pass++) {
    for (let l = 1; l < byLayer.length; l++) {
      byLayer[l].sort(
        (a, b) => barycenter(a) - barycenter(b) || a.label.localeCompare(b.label),
      );
    }
    setPos();
  }
}

export function layoutGraph(
  nodes: GraphNode[],
  links: GraphLink[],
  opts: LayoutOptions,
): GraphLayout {
  const { nodeW, nodeH } = opts;
  const colGap = opts.colGap ?? COL_GAP;
  const baseRowGap = opts.rowGap ?? ROW_GAP;
  const maxRowGap = opts.maxRowGap ?? MAX_ROW_GAP;
  if (nodes.length === 0) {
    return { nodes: [], byId: new Map(), links: [], width: 0, height: 0, layers: 0 };
  }
  const present = new Set(nodes.map((n) => n.id));
  const visible = links.filter((e) => present.has(e.from) && present.has(e.to));

  const backEdges = findBackEdges(nodes, visible);
  const forward = visible.filter((e) => !backEdges.has(e.key));
  const layerOf = assignLayers(nodes, forward);
  const maxLayer = Math.max(...nodes.map((n) => layerOf.get(n.id) ?? 0));
  const byLayer: Slot[][] = Array.from({ length: maxLayer + 1 }, () => []);
  const slots = new Map<string, Slot>();

  function addSlot(layer: number, slot: Slot) {
    byLayer[layer].push(slot);
    slots.set(slot.id, slot);
  }

  for (const n of [...nodes].sort((a, b) => a.label.localeCompare(b.label))) {
    const l = layerOf.get(n.id) ?? 0;
    addSlot(l, {
      id: n.id,
      real: true,
      label: n.label,
      layer: l,
      index: 0,
      x: 0,
      y: 0,
      h: n.height ?? nodeH,
    });
  }

  // Break every layer-skipping edge into a chain of waypoints, one per column
  // it crosses. Back edges are left alone — they already bow clear of the
  // layout, and threading them through it would hide the cycle they exist to
  // announce.
  const chains = new Map<string, string[]>();
  for (const e of visible) {
    if (e.from === e.to) continue;
    const lf = layerOf.get(e.from) ?? 0;
    const lt = layerOf.get(e.to) ?? 0;
    const chain = [e.from];
    for (let l = lf + 1; l < lt && !backEdges.has(e.key); l++) {
      const id = `${e.key}@${l}`;
      addSlot(l, { id, real: false, label: id, layer: l, index: 0, x: 0, y: 0, h: CHANNEL_H });
      chain.push(id);
    }
    chain.push(e.to);
    chains.set(e.key, chain);
  }

  const preds = new Map<string, string[]>();
  for (const chain of chains.values()) {
    for (let i = 1; i < chain.length; i++) {
      const a = slots.get(chain[i - 1]);
      const b = slots.get(chain[i]);
      if (a && b && a.layer < b.layer) {
        preds.set(b.id, [...(preds.get(b.id) ?? []), a.id]);
      }
    }
  }

  orderLayers(byLayer, preds);

  // Slots have different heights, so a layer's extent is a sum rather than a
  // count. Short layers centre against the tallest one so the graph reads as a
  // shape rather than everything jammed to the top.
  const extent = (list: Slot[], gap: number) =>
    list.reduce((sum, s) => sum + s.h, 0) + Math.max(0, list.length - 1) * gap;

  let rowGap = baseRowGap;
  const natural = Math.max(...byLayer.map((l) => extent(l, baseRowGap)));
  const gaps = Math.max(...byLayer.map((l) => Math.max(0, l.length - 1)));
  const minHeight = opts.minHeight ?? 0;
  if (minHeight > natural + PAD * 2 && gaps > 0) {
    rowGap = Math.min(maxRowGap, baseRowGap + (minHeight - PAD * 2 - natural) / gaps);
  }

  const tallest = Math.max(...byLayer.map((l) => extent(l, rowGap)));

  byLayer.forEach((list, l) => {
    let y = PAD + (tallest - extent(list, rowGap)) / 2;
    list.forEach((s, i) => {
      s.index = i;
      s.x = PAD + l * (nodeW + colGap);
      s.y = y;
      y += s.h + rowGap;
    });
  });

  const placed: PlacedNode[] = [];
  const byId = new Map<string, PlacedNode>();
  for (const list of byLayer) {
    for (const s of list) {
      if (!s.real) continue;
      const n: PlacedNode = {
        id: s.id,
        layer: s.layer,
        index: s.index,
        x: s.x,
        y: s.y,
        w: nodeW,
        h: s.h,
      };
      placed.push(n);
      byId.set(s.id, n);
    }
  }

  const placedLinks: PlacedLink[] = [];
  for (const e of visible) {
    const from = byId.get(e.from);
    const to = byId.get(e.to);
    if (!from || !to || from === to) continue;
    const back = backEdges.has(e.key) || to.layer <= from.layer;
    const chain = chains.get(e.key) ?? [e.from, e.to];
    const points: Point[] = [{ x: from.x + from.w, y: from.y + from.h / 2 }];
    for (const id of chain.slice(1, -1)) {
      const s = slots.get(id);
      if (s) points.push({ x: s.x + nodeW / 2, y: s.y + s.h / 2 });
    }
    points.push({ x: to.x, y: to.y + to.h / 2 });
    placedLinks.push({ key: e.key, from, to, back, points });
  }

  return {
    nodes: placed,
    byId,
    links: placedLinks,
    width: PAD * 2 + (maxLayer + 1) * nodeW + maxLayer * colGap,
    height: PAD * 2 + tallest,
    layers: maxLayer + 1,
  };
}

// The bow a back edge takes above the layout, in pixels. Shared by the path and
// its label so a count sits on the curve instead of near it.
function backBow(e: PlacedLink): number {
  return 26 + Math.abs(e.from.layer - e.to.layer) * 8;
}

// linkPath draws the route as a chain of cubics with horizontal tangents, so a
// routed edge leaves and enters every column flat and the joins between
// segments are invisible. Back edges bow outward instead, so they're
// distinguishable from the forward flow rather than overlapping it.
export function linkPath(e: PlacedLink): string {
  if (e.back) {
    const bow = backBow(e);
    const y1 = e.from.y + e.from.h / 2;
    const y2 = e.to.y + e.to.h / 2;
    const midY = Math.min(y1, y2) - bow;
    const x2 = e.to.x;
    return `M ${e.from.x} ${y1} C ${e.from.x - bow} ${midY}, ${x2 + e.to.w + bow} ${midY}, ${
      x2 + e.to.w
    } ${y2}`;
  }
  const p = e.points;
  let d = `M ${p[0].x} ${p[0].y}`;
  for (let i = 1; i < p.length; i++) {
    const a = p[i - 1];
    const b = p[i];
    const dx = Math.max(24, (b.x - a.x) / 2);
    d += ` C ${a.x + dx} ${a.y}, ${b.x - dx} ${b.y}, ${b.x} ${b.y}`;
  }
  return d;
}

// Where an edge's label goes. Never over a node: a routed edge has a waypoint
// sitting in a channel that was reserved clear of them, and a single-span edge
// is labelled in the gap between two columns, which no node occupies either.
// This is the fix for counts landing on top of the very nodes you were hovering
// to read.
export function linkLabelPoint(e: PlacedLink): Point {
  if (e.back) {
    const bow = backBow(e);
    return {
      x: (e.from.x + e.to.x + e.to.w) / 2,
      y: Math.min(e.from.y, e.to.y) + e.from.h / 2 - bow * 0.75,
    };
  }
  const p = e.points;
  const inner = p.slice(1, -1);
  // Any waypoint is in a reserved channel, so prefer the middle one outright.
  if (inner.length > 0) return inner[Math.floor((inner.length - 1) / 2)];
  return { x: (p[0].x + p[1].x) / 2, y: (p[0].y + p[1].y) / 2 };
}
