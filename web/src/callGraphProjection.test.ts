import { describe, expect, test } from "bun:test";
import {
  bucketsOf,
  EXTERNAL_GROUP,
  project,
  servicesOnChain,
  visibleNodes,
  withinHops,
  type Visibility,
} from "./callGraphProjection";
import type { CallGraph, CallGraphEdge, CallGraphNode } from "./types";

// The arithmetic of folding a graph down. Every bug this catches is a wrong
// number on a node that still renders perfectly well — a count saying 14 when
// three are showing, two calls drawn as one line without saying so, a call
// that vanished into a service that shouldn't have swallowed it.

function node(over: Partial<CallGraphNode> & { id: string }): CallGraphNode {
  return {
    origin: "entrypoint",
    title: over.key ?? over.id,
    outboundKnown: true,
    ...over,
  } as CallGraphNode;
}

function edge(from: string, to: string, over: Partial<CallGraphEdge> = {}): CallGraphEdge {
  return { from, to, kind: "grpc.method", key: `${from}->${to}`, sites: [{}], ...over };
}

// orders serves two APIs, billing one, and one call leaves the workspace.
const graph: CallGraph = {
  nodes: [
    node({ id: "a", service: "orders", key: "POST /orders", entry: true }),
    node({ id: "b", service: "orders", key: "POST /refunds" }),
    node({ id: "c", service: "billing", key: "Billing/Charge" }),
    node({ id: "d", origin: "external", key: "stripe/Charge" }),
  ],
  edges: [
    edge("a", "b"), // inside orders
    edge("a", "c"),
    edge("b", "c"),
    edge("c", "d"),
  ],
};

const wide: Visibility = { filter: "", anchored: false, onlyReaching: false, focus: null, hops: 2 };
const all = (g: CallGraph) => visibleNodes(g, wide);

describe("collapsing to services", () => {
  test("a collapsed service is one node standing for its APIs", () => {
    const p = project(graph, all(graph), new Set());
    expect(p.nodes.map((n) => n.id).sort()).toEqual([
      "svc:billing",
      "svc:orders",
      `svc:${EXTERNAL_GROUP}`,
    ]);
    const orders = p.nodes.find((n) => n.id === "svc:orders")!;
    expect(orders.group?.apis).toBe(2);
    expect(orders.group?.entries).toBe(1);
  });

  test("a call between two APIs of one collapsed service disappears into it", () => {
    // a→b is internal to orders. Drawn as a self-loop it would be noise; drawn
    // as an edge between two nodes that are now one node, it would be a lie.
    const p = project(graph, all(graph), new Set());
    expect(p.edges.some((e) => e.from === e.to)).toBe(false);
    expect(p.edges.map((e) => e.key).sort()).toEqual([
      "svc:billing->svc:~external",
      "svc:orders->svc:billing",
    ]);
  });

  test("parallel calls merge into one line that says how many", () => {
    // Both of orders' APIs call billing. One line, count 2 — a bundle and a
    // single call must not look the same.
    const p = project(graph, all(graph), new Set());
    const e = p.edges.find((x) => x.key === "svc:orders->svc:billing")!;
    expect(e.calls).toBe(2);
    expect(e.bundled).toBe(true);
    // No single underlying call, so there is nothing to open.
    expect(e.only).toBeUndefined();
  });

  test("a bundle of one keeps the call it stands for, so it stays openable", () => {
    const p = project(graph, all(graph), new Set());
    const e = p.edges.find((x) => x.key === "svc:billing->svc:~external")!;
    expect(e.calls).toBe(1);
    expect(e.only?.from).toBe("c");
  });

  test("expanding one service shows its APIs and leaves the rest folded", () => {
    // The mixed state is the one worth having: real APIs where you are
    // reading, collapsed context everywhere else.
    const p = project(graph, all(graph), new Set(["orders"]));
    expect(p.nodes.map((n) => n.id).sort()).toEqual([
      "a",
      "b",
      "svc:billing",
      `svc:${EXTERNAL_GROUP}`,
    ]);
    // Now that orders is open, its internal call is a real edge again.
    expect(p.edges.some((e) => e.from === "a" && e.to === "b")).toBe(true);
    // And both of its APIs point at billing separately.
    expect(p.edges.filter((e) => e.to === "svc:billing").length).toBe(2);
  });

  test("external keys collapse into one bucket rather than N dead ends", () => {
    const p = project(graph, all(graph), new Set());
    const ext = p.nodes.find((n) => n.id === `svc:${EXTERNAL_GROUP}`)!;
    expect(ext.label).toBe("outside the workspace");
    expect(ext.group?.apis).toBe(1);
  });

  test("a group is an entry point only when everything in it is", () => {
    // One of orders' two APIs is an entry. Marking the service an entry point
    // on that basis would mark every service in a platform.
    const p = project(graph, all(graph), new Set());
    expect(p.nodes.find((n) => n.id === "svc:orders")!.entry).toBe(false);

    const solo: CallGraph = { nodes: [graph.nodes[0]], edges: [] };
    const q = project(solo, all(solo), new Set());
    expect(q.nodes[0].entry).toBe(true);
  });
});

describe("counts describe the view, not the workspace", () => {
  test("a filtered group counts what survived the filter", () => {
    const visible = visibleNodes(graph, { ...wide, filter: "refunds" });
    const p = project(graph, visible, new Set());
    expect(p.nodes.length).toBe(1);
    expect(p.nodes[0].group?.apis).toBe(1);
    expect(p.hiddenByFilter).toBe(3);
  });

  test("an anchor keeps only the chain reaching it", () => {
    const anchored: CallGraph = {
      ...graph,
      nodes: graph.nodes.map((n) => (n.id === "a" || n.id === "c" ? { ...n, reachesAnchor: true } : n)),
    };
    const visible = visibleNodes(anchored, { ...wide, anchored: true, onlyReaching: true });
    expect([...visible].sort()).toEqual(["a", "c"]);
    // And the services on that chain are the ones worth opening on arrival.
    expect(servicesOnChain(anchored).sort()).toEqual(["billing", "orders"]);
  });
});

describe("focus is bounded", () => {
  // a → b → c → d → e, focused on a. Unbounded, focus returns the whole
  // chain — which on a dense graph is most of the graph, so focus stops
  // narrowing exactly when it is needed.
  const chain: CallGraph = {
    nodes: ["a", "b", "c", "d", "e"].map((id) => node({ id, service: id })),
    edges: [edge("a", "b"), edge("b", "c"), edge("c", "d"), edge("d", "e")],
  };

  test("one hop keeps only the immediate neighbours", () => {
    expect([...withinHops("b", chain.edges, 1)].sort()).toEqual(["a", "b", "c"]);
  });

  test("two hops reaches two steps in both directions", () => {
    expect([...withinHops("c", chain.edges, 2)].sort()).toEqual(["a", "b", "c", "d", "e"]);
    expect([...withinHops("a", chain.edges, 2)].sort()).toEqual(["a", "b", "c"]);
  });

  test("a focus overrides the anchor filter rather than intersecting it", () => {
    // Focusing is an explicit "show me this instead", so it must not be
    // silently emptied by a narrowing the user set earlier and forgot.
    const visible = visibleNodes(chain, {
      ...wide,
      anchored: true,
      onlyReaching: true, // nothing is marked, so this alone would hide everything
      focus: "b",
      hops: 1,
    });
    expect([...visible].sort()).toEqual(["a", "b", "c"]);
  });

  test("widening the hop count reaches further", () => {
    const near = visibleNodes(chain, { ...wide, focus: "a", hops: 1 });
    const far = visibleNodes(chain, { ...wide, focus: "a", hops: 4 });
    expect(near.size).toBe(2);
    expect(far.size).toBe(5);
  });
});

describe("buckets", () => {
  test("every service is nameable, with the external one last", () => {
    expect(bucketsOf(graph)).toEqual([
      { service: "billing", apis: 1 },
      { service: "orders", apis: 2 },
      { service: EXTERNAL_GROUP, apis: 1 },
    ]);
  });
});
