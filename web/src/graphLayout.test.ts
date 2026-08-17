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

// Crossing count, computed from the routes the layout actually returns rather
// than from anything it reports about itself.
function crossings(links: { points: { x: number; y: number }[] }[]): number {
  const side = (p: any, q: any, r: any) =>
    Math.sign((q.x - p.x) * (r.y - p.y) - (q.y - p.y) * (r.x - p.x));
  const hits = (a: any, b: any, c: any, d: any) =>
    side(a, b, c) !== side(a, b, d) && side(c, d, a) !== side(c, d, b);
  let n = 0;
  for (let i = 0; i < links.length; i++)
    for (let j = i + 1; j < links.length; j++) {
      let crossed = false;
      for (let k = 0; k < links[i].points.length - 1 && !crossed; k++)
        for (let l = 0; l < links[j].points.length - 1 && !crossed; l++)
          if (hits(links[i].points[k], links[i].points[k + 1], links[j].points[l], links[j].points[l + 1]))
            crossed = true;
      if (crossed) n++;
    }
  return n;
}

describe("crossing reduction", () => {
  // The service-level graph of a twelve-service workspace, taken from a real
  // run rather than invented: small synthetic graphs are too symmetric to tell
  // the orderings apart, and inventing a baseline number is how you end up
  // asserting something you never measured.
  //
  // Ordering by predecessors alone scored 653 crossing pairs on this graph.
  // The leftmost layer has no predecessors to be ordered by, so it stayed in
  // label order — and that layer is where the fan-out deciding the whole
  // picture lives. Alternating the sweeps, so a layer can also be ordered by
  // where its successors ended up, brought it to 452.
  const EDGES: [string, string, number][] = [
    ["accounts","analytics",1],["accounts","audit",2],["accounts","billing",4],
    ["accounts","fulfilment",4],["accounts","inventory",1],["accounts","notifications",4],
    ["accounts","orders",3],["accounts","pricing",2],["accounts","search",2],
    ["analytics","audit",8],["analytics","pricing",8],["audit","pricing",10],
    ["billing","analytics",3],["billing","audit",4],["billing","inventory",4],
    ["billing","notifications",3],["billing","orders",4],["billing","pricing",3],
    ["fulfilment","analytics",1],["fulfilment","audit",3],["fulfilment","notifications",6],
    ["fulfilment","pricing",7],["fulfilment","search",3],["gateway","accounts",3],
    ["gateway","analytics",3],["gateway","audit",2],["gateway","billing",1],
    ["gateway","identity",5],["gateway","inventory",2],["gateway","orders",3],
    ["gateway","pricing",2],["identity","accounts",2],["identity","analytics",4],
    ["identity","audit",2],["identity","billing",4],["identity","notifications",2],
    ["identity","orders",1],["identity","pricing",4],["identity","search",2],
    ["inventory","analytics",5],["inventory","audit",2],["inventory","fulfilment",5],
    ["inventory","notifications",4],["inventory","pricing",5],["inventory","search",5],
    ["notifications","analytics",2],["notifications","audit",6],["notifications","pricing",8],
    ["notifications","search",3],["orders","analytics",3],["orders","audit",6],
    ["orders","fulfilment",1],["orders","inventory",1],["orders","notifications",3],
    ["orders","pricing",3],["search","analytics",6],["search","audit",7],
    ["search","pricing",7],
  ];

  const workspace = () => {
    const ids = [...new Set(EDGES.flatMap(([a, b]) => [a, b]))].sort();
    return {
      nodes: ids.map((id) => ({ id, label: id })),
      links: EDGES.map(([from, to, weight]) => ({ key: `${from}->${to}`, from, to, weight })),
    };
  };

  test("a real dense workspace stays well under what one direction left behind", () => {
    // An upper bound, not an equality: a better ordering is not a regression,
    // and pinning the exact number would fail the next time this improves.
    const { nodes, links } = workspace();
    const g = layoutGraph(nodes, links, { nodeW: 208, nodeH: 56, minHeight: 680 });
    expect(crossings(g.links)).toBeLessThan(550);
  });

  test("keeping the best sweep means input order can't change the result", () => {
    // Barycenter is a heuristic and an individual sweep can degrade a layout,
    // so the passes keep the best ordering they saw rather than the last. Two
    // orderings of the same graph must therefore agree.
    const { nodes, links } = workspace();
    const opts = { nodeW: 208, nodeH: 56, minHeight: 680 };
    const a = layoutGraph(nodes, links, opts);
    const b = layoutGraph([...nodes].reverse(), [...links].reverse(), opts);
    expect(crossings(b.links)).toBe(crossings(a.links));
  });
});
