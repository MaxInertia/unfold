import { describe, expect, test } from "bun:test";
import {
  layoutGraph,
  linkLabelPoint,
  type GraphLink,
  type GraphNode,
  type PlacedLink,
  type PlacedNode,
} from "./graphLayout";

// The properties this layout exists to guarantee, asserted numerically rather
// than by eye.
//
// The failure these pin is specific and was shipped once: with A→B→C and A→C,
// the skipping edge was drawn as a straight cubic through B's column and
// arrived at C from the same direction B's edge did. The picture read as "A
// stops at B" — the graph lying about the dependency, which is the one thing a
// dependency graph must not do. Every case below is really the same question:
// does a line ever pass through a box.

const OPTS = { nodeW: 100, nodeH: 40 };

function nodes(...ids: string[]): GraphNode[] {
  return ids.map((id) => ({ id, label: id }));
}

function links(...pairs: [string, string, number?][]): GraphLink[] {
  return pairs.map(([from, to, weight]) => ({ key: `${from}->${to}`, from, to, weight: weight ?? 1 }));
}

// Whether a point sits inside a node's box, with a little slack so a route
// grazing an edge still counts as a collision.
function inside(p: { x: number; y: number }, n: PlacedNode, slack = 2): boolean {
  return (
    p.x > n.x - slack &&
    p.x < n.x + n.w + slack &&
    p.y > n.y - slack &&
    p.y < n.y + n.h + slack
  );
}

// Every waypoint of every route, excluding the endpoints, which are meant to
// touch their own nodes.
function waypoints(link: PlacedLink) {
  return link.points.slice(1, -1);
}

describe("layered layout", () => {
  test("depth is one past the deepest node pointing at you", () => {
    const g = layoutGraph(nodes("a", "b", "c"), links(["a", "b"], ["b", "c"]), OPTS);
    expect(g.byId.get("a")!.layer).toBe(0);
    expect(g.byId.get("b")!.layer).toBe(1);
    expect(g.byId.get("c")!.layer).toBe(2);
    expect(g.layers).toBe(3);
  });

  test("a layer-skipping edge is routed around the column it skips", () => {
    const g = layoutGraph(nodes("a", "b", "c"), links(["a", "b"], ["b", "c"], ["a", "c"]), OPTS);
    const skip = g.links.find((l) => l.key === "a->c")!;
    const b = g.byId.get("b")!;

    // It has a waypoint at all: a straight line is the bug.
    expect(waypoints(skip).length).toBe(1);
    for (const p of waypoints(skip)) expect(inside(p, b)).toBe(false);
  });

  test("no route passes through any node, across a four-layer skip", () => {
    const g = layoutGraph(
      nodes("a", "b", "c", "d", "e"),
      links(["a", "b"], ["b", "c"], ["c", "d"], ["d", "e"], ["a", "e"]),
      OPTS,
    );
    const skip = g.links.find((l) => l.key === "a->e")!;
    expect(waypoints(skip).length).toBe(3); // one per column crossed

    for (const link of g.links) {
      for (const p of waypoints(link)) {
        for (const n of g.nodes) expect(inside(p, n)).toBe(false);
      }
    }
  });

  test("an edge label never lands on a node", () => {
    const g = layoutGraph(
      nodes("a", "b", "c", "d"),
      links(["a", "b"], ["b", "c"], ["c", "d"], ["a", "d"]),
      OPTS,
    );
    for (const link of g.links) {
      const p = linkLabelPoint(link);
      for (const n of g.nodes) expect(inside(p, n)).toBe(false);
    }
  });
});

describe("cycles", () => {
  test("a cycle is broken before layering, so forward edges stay short", () => {
    // gateway → orders → billing → ledger, with ledger calling back to orders.
    // Handled during layering instead, the back edge pushes orders past both
    // of them and the gateway→orders edge snakes the width of the graph to
    // reach a node one hop away.
    const g = layoutGraph(
      nodes("gateway", "orders", "billing", "ledger"),
      links(
        ["gateway", "orders", 5],
        ["orders", "billing", 3],
        ["billing", "ledger", 3],
        ["ledger", "orders", 1],
      ),
      OPTS,
    );
    expect(g.byId.get("gateway")!.layer).toBe(0);
    expect(g.byId.get("orders")!.layer).toBe(1);
    expect(g.byId.get("billing")!.layer).toBe(2);
    expect(g.byId.get("ledger")!.layer).toBe(3);

    const back = g.links.filter((l) => l.back).map((l) => l.key);
    expect(back).toEqual(["ledger->orders"]);
  });

  test("the lightest edge of a cycle is the one that bows backwards", () => {
    // Between a call carrying twelve RPCs and a single callback, the callback
    // is what should bow. Picking by DFS arrival order instead marks whichever
    // edge the walk happened to reach second, which on this graph is the busy
    // one — the same wrong answer that made a three-RPC edge the "cycle".
    const g = layoutGraph(
      nodes("a", "b"),
      links(["a", "b", 12], ["b", "a", 1]),
      OPTS,
    );
    const back = g.links.filter((l) => l.back).map((l) => l.key);
    expect(back).toEqual(["b->a"]);
  });

  test("a self-edge is dropped rather than laid out", () => {
    const g = layoutGraph(nodes("a", "b"), links(["a", "b"], ["a", "a"]), OPTS);
    expect(g.links.map((l) => l.key)).toEqual(["a->b"]);
  });
});

describe("sizing", () => {
  test("spare height is handed back as row gap, up to a ceiling", () => {
    const short = layoutGraph(nodes("a", "b", "c"), [], OPTS);
    const tall = layoutGraph(nodes("a", "b", "c"), [], { ...OPTS, minHeight: 2000 });
    expect(tall.height).toBeGreaterThan(short.height);
    // Never unbounded: the ceiling is what stops related nodes drifting so far
    // apart they read as unrelated.
    expect(tall.height).toBeLessThan(2000);
  });

  test("the layout never shrinks below its natural size", () => {
    const natural = layoutGraph(nodes("a", "b", "c"), [], OPTS);
    const squeezed = layoutGraph(nodes("a", "b", "c"), [], { ...OPTS, minHeight: 10 });
    expect(squeezed.height).toBe(natural.height);
  });

  test("an empty graph lays out to nothing rather than throwing", () => {
    const g = layoutGraph([], [], OPTS);
    expect(g.nodes).toEqual([]);
    expect(g.width).toBe(0);
  });

  test("nodes carry their own height when given one", () => {
    const g = layoutGraph(
      [
        { id: "a", label: "a", height: 90 },
        { id: "b", label: "b" },
      ],
      links(["a", "b"]),
      OPTS,
    );
    expect(g.byId.get("a")!.h).toBe(90);
    expect(g.byId.get("b")!.h).toBe(40);
    // The edge leaves the tall node at *its* middle, not at a constant one.
    const link = g.links[0];
    expect(link.points[0].y).toBe(g.byId.get("a")!.y + 45);
  });
});

describe("determinism", () => {
  test("the same graph lays out identically however the input is ordered", () => {
    const a = layoutGraph(
      nodes("a", "b", "c", "d"),
      links(["a", "b"], ["a", "c"], ["b", "d"], ["c", "d"]),
      OPTS,
    );
    const b = layoutGraph(
      nodes("d", "c", "b", "a"),
      links(["c", "d"], ["b", "d"], ["a", "c"], ["a", "b"]),
      OPTS,
    );
    const at = a.nodes.map((n) => [n.id, n.x, n.y]).sort();
    const bt = b.nodes.map((n) => [n.id, n.x, n.y]).sort();
    expect(at).toEqual(bt);
  });
});
