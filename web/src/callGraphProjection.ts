import type { CallGraph, CallGraphEdge, CallGraphNode } from "./types";

// What the call graph view actually draws, which is not what the API returns.
//
// The API returns every API in the workspace. Drawn literally that is 168 nodes
// and 214 edges on a twelve-service workspace — a canvas six times taller than
// the window, one column holding 72 entry points, and 2662 crossing edge pairs.
// Every node was individually correct and the picture was useless, which is the
// failure mode the platform doc predicted for L1 and this view inherited by
// being finer-grained than L0 rather than coarser.
//
// The fix is not a better layout. No layout makes 168 nodes legible at once;
// the fix is not drawing 168 nodes. A service collapses to a single node
// standing for its APIs, and you expand the ones you are actually reading —
// so the default view is at most one node per service, and the mixed state
// (orders expanded, everything else collapsed) is the one that answers real
// questions: "what does *this* service's surface do in the context of the
// platform".
//
// Kept apart from the component and pure, because the interesting failures are
// arithmetic — an edge that should have merged and didn't, a count that says
// 14 when 3 are showing — and those are worth testing without a browser.

// The bucket external keys collapse into. They belong to no service, and one
// node saying "eleven keys leave the workspace here" is the honest summary;
// eleven separate dead ends is the noise this whole file exists to remove.
export const EXTERNAL_GROUP = "~external";

export interface Visibility {
  filter: string;
  // With an anchor, the chain reaching it is the point and the rest starts
  // hidden. Ignored when nothing is anchored.
  anchored: boolean;
  onlyReaching: boolean;
  // The node the view is focused on, and how many hops out from it to keep.
  focus: string | null;
  hops: number;
}

export interface ProjectedNode {
  id: string;
  // Set when this node stands for a whole service rather than one API.
  group?: {
    service: string; // alias, or EXTERNAL_GROUP
    apis: number; // how many APIs it stands for *in this view*
    entries: number;
    reaching: number;
    unread: number;
  };
  // Set when this node is one API.
  node?: CallGraphNode;
  label: string;
  sub: string;
  entry: boolean;
  reachesAnchor: boolean;
}

export interface ProjectedEdge {
  key: string;
  from: string;
  to: string;
  // How many underlying edges this one stands for, and how many call sites
  // are behind all of them. A bundle of nine calls and a single call must not
  // look the same.
  calls: number;
  sites: number;
  fanout: boolean;
  inferred: boolean;
  reachesAnchor: boolean;
  // At least one end is collapsed, so this line is a summary rather than a
  // call you can open.
  bundled: boolean;
  // The one underlying edge, when there is exactly one. What makes a bundled
  // graph still openable: collapse a service and its single call to another
  // service is still that call.
  only?: CallGraphEdge;
}

export interface Projection {
  nodes: ProjectedNode[];
  edges: ProjectedEdge[];
  // How much was folded away, so the view can say so rather than looking like
  // a workspace with twelve APIs in it.
  hiddenByFilter: number;
  collapsedServices: number;
}

// Which service bucket a node belongs to. Root and entrypoint nodes belong to
// their own service; an external key belongs to nobody, which is itself a
// bucket.
export function bucketOf(n: CallGraphNode): string {
  return n.service || EXTERNAL_GROUP;
}

function groupId(service: string): string {
  return `svc:${service}`;
}

// The set of nodes a focus keeps: everything within `hops` of it, in either
// direction.
//
// Bounded, unlike the first version. On a sparse graph the unbounded two-way
// closure is the chain you wanted; on a dense one it is most of the graph,
// which makes focus useless exactly when it is needed. Two hops is the default
// because it answers "what calls this, and what does that call" — the question
// people actually ask at a node — and the control widens it.
export function withinHops(
  id: string,
  edges: CallGraphEdge[],
  hops: number,
): Set<string> {
  const out = new Map<string, string[]>();
  const inc = new Map<string, string[]>();
  for (const e of edges) {
    out.set(e.from, [...(out.get(e.from) ?? []), e.to]);
    inc.set(e.to, [...(inc.get(e.to) ?? []), e.from]);
  }
  const seen = new Set<string>([id]);
  for (const dir of [out, inc]) {
    let frontier = [id];
    for (let d = 0; d < hops && frontier.length > 0; d++) {
      const next: string[] = [];
      for (const cur of frontier) {
        for (const n of dir.get(cur) ?? []) {
          if (seen.has(n)) continue;
          seen.add(n);
          next.push(n);
        }
      }
      frontier = next;
    }
  }
  return seen;
}

// Which API nodes survive the filters, before any grouping. Grouping happens
// after, so a group's counts describe what is actually in the view rather than
// what the workspace holds — "3 APIs" under a filter that matched three.
export function visibleNodes(graph: CallGraph, v: Visibility): Set<string> {
  const q = v.filter.trim().toLowerCase();
  const scoped = v.focus ? withinHops(v.focus, graph.edges, v.hops) : null;
  const ids = new Set<string>();
  for (const n of graph.nodes) {
    if (scoped) {
      if (scoped.has(n.id)) ids.add(n.id);
      continue;
    }
    if (v.anchored && v.onlyReaching && !n.reachesAnchor) continue;
    if (q) {
      const hay = `${n.title} ${n.service ?? ""} ${n.kind ?? ""}`.toLowerCase();
      if (!hay.includes(q)) continue;
    }
    ids.add(n.id);
  }
  return ids;
}

export function project(
  graph: CallGraph,
  visible: Set<string>,
  expanded: Set<string>,
): Projection {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));

  // Where each visible node is drawn: itself when its service is expanded,
  // otherwise its service's group node.
  const rep = new Map<string, string>();
  const members = new Map<string, CallGraphNode[]>();
  for (const id of visible) {
    const n = byId.get(id);
    if (!n) continue;
    const bucket = bucketOf(n);
    if (expanded.has(bucket)) {
      rep.set(id, id);
      continue;
    }
    const gid = groupId(bucket);
    rep.set(id, gid);
    members.set(gid, [...(members.get(gid) ?? []), n]);
  }

  const nodes: ProjectedNode[] = [];
  for (const id of visible) {
    const n = byId.get(id);
    if (!n || rep.get(id) !== id) continue;
    nodes.push({
      id,
      node: n,
      label: n.title,
      sub: n.service ?? "",
      entry: !!n.entry,
      reachesAnchor: !!n.reachesAnchor,
    });
  }
  for (const [gid, list] of members) {
    const service = gid.slice("svc:".length);
    nodes.push({
      id: gid,
      group: {
        service,
        apis: list.length,
        entries: list.filter((n) => n.entry).length,
        reaching: list.filter((n) => n.reachesAnchor).length,
        unread: list.filter((n) => !n.outboundKnown && n.origin !== "external").length,
      },
      label: service === EXTERNAL_GROUP ? "outside the workspace" : service,
      sub: `${list.length} API${list.length === 1 ? "" : "s"}`,
      // A group is an entry point only if *nothing* it holds is reached from
      // outside it. Saying "entry" because one of fourteen APIs is one would
      // mark every service in the platform.
      entry: list.every((n) => n.entry),
      reachesAnchor: list.some((n) => n.reachesAnchor),
    });
  }

  // Edges follow their endpoints. Two calls between the same pair of collapsed
  // services are one line carrying a count, and a call whose ends land in the
  // same collapsed service disappears into it — that is the whole saving.
  const merged = new Map<string, ProjectedEdge>();
  for (const e of graph.edges) {
    const from = rep.get(e.from);
    const to = rep.get(e.to);
    if (!from || !to || from === to) continue;
    const key = `${from}->${to}`;
    const cur = merged.get(key);
    const sites = e.sites?.length ?? 1;
    if (cur) {
      cur.calls += 1;
      cur.sites += sites;
      cur.fanout ||= !!e.fanout;
      cur.inferred ||= e.confidence === "inferred";
      cur.reachesAnchor ||= !!e.reachesAnchor;
      cur.only = undefined; // more than one now, so no single call to open
    } else {
      merged.set(key, {
        key,
        from,
        to,
        calls: 1,
        sites,
        fanout: !!e.fanout,
        inferred: e.confidence === "inferred",
        reachesAnchor: !!e.reachesAnchor,
        bundled: from.startsWith("svc:") || to.startsWith("svc:"),
        only: e,
      });
    }
  }

  return {
    nodes,
    edges: [...merged.values()],
    hiddenByFilter: graph.nodes.length - visible.size,
    collapsedServices: members.size,
  };
}

// Every service bucket the graph holds, in display order, with how many APIs
// each has. The header's expand/collapse controls render from this, so a
// service you have never expanded is still nameable.
export function bucketsOf(graph: CallGraph): { service: string; apis: number }[] {
  const counts = new Map<string, number>();
  for (const n of graph.nodes) {
    const b = bucketOf(n);
    counts.set(b, (counts.get(b) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([service, apis]) => ({ service, apis }))
    .sort((a, b) =>
      // The external bucket last: it is the edge of the workspace, not a
      // service you would go looking for.
      a.service === EXTERNAL_GROUP
        ? 1
        : b.service === EXTERNAL_GROUP
          ? -1
          : a.service.localeCompare(b.service),
    );
}

// The services an anchor's chain runs through, which are the ones worth
// expanding on arrival: collapsed context everywhere else, real APIs where the
// answer is.
export function servicesOnChain(graph: CallGraph): string[] {
  const out = new Set<string>();
  for (const n of graph.nodes) if (n.reachesAnchor) out.add(bucketOf(n));
  return [...out];
}
