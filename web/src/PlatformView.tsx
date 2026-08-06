import { useEffect, useMemo, useState } from "react";
import { fetchPlatformView, indexRepo } from "./api";
import { edgePath, labelPoint, layout, NODE_H, NODE_W } from "./platformLayout";
import type { PlatformEdge, PlatformService, PlatformView as PlatformViewT, TargetID } from "./types";

// The L0 (platform) zoom level: every service in the workspace and the calls
// between them.
//
// This is where node-link rendering earns its place: the node count is the
// number of services, not the number of routes. The overview is a layered
// graph — dependency direction left to right — so the shape itself carries
// the information a grid of count badges couldn't: which services are entry
// points, which are shared leaves, where the depth and the cycles are.
//
// Picking a service still drops to the slice around it — callers, it,
// callees, with the individual RPCs on each edge — because that answers a
// different question and stays readable however large the workspace grows.
export function PlatformView({
  anchor,
  filter,
  selected,
  onSelect,
  onOpenService,
  onOpenSite,
}: {
  // The frame zoomed out from. Carried all the way up: at this level it marks
  // the services whose calls lead to it.
  anchor: TargetID | null;
  filter: string;
  selected: string | null;
  onSelect: (alias: string | null) => void;
  onOpenService: (alias: string) => void;
  onOpenSite: (id: TargetID) => void;
}) {
  const [view, setView] = useState<PlatformViewT | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let alive = true;
    fetchPlatformView(anchor)
      .then((v) => alive && setView(v))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, [anchor, revision]);

  async function index(alias: string) {
    setBusy(alias);
    try {
      await indexRepo(alias);
      setRevision((n) => n + 1);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  if (error) return <div className="app-error">{error}</div>;
  if (!view) return <div className="app-loading">loading platform…</div>;

  const q = filter.trim().toLowerCase();
  const services = view.services.filter((s) => !q || s.name.toLowerCase().includes(q));
  const unindexed = view.services.filter((s) => !s.indexed).length;
  const reaching = view.services.filter((s) => s.reachesAnchor).length;

  return (
    <div className="platform">
      <header className="platform-header">
        <h2 className="platform-title">workspace</h2>
        <span className="platform-counts">
          {view.services.length} service{view.services.length === 1 ? "" : "s"} ·{" "}
          {view.edges.length} edge{view.edges.length === 1 ? "" : "s"}
        </span>
        {view.anchorTitle && (
          <span className="service-anchor" title="the frame you zoomed out from">
            anchored on <b>{view.anchorTitle}</b>
            {" · "}
            {reaching} service{reaching === 1 ? "" : "s"} reach it
          </span>
        )}
        {/* Say what's missing rather than letting an unindexed service read
            as one that calls nothing. */}
        {unindexed > 0 && (
          <span className="platform-warn">
            {unindexed} not indexed — their outgoing calls are unknown until they are
          </span>
        )}
      </header>

      {selected ? (
        <Slice
          view={view}
          alias={selected}
          onSelect={onSelect}
          onOpenService={onOpenService}
          onOpenSite={onOpenSite}
        />
      ) : services.length === 0 ? (
        <p className="platform-empty">No service matches “{filter}”.</p>
      ) : (
        <Graph
          services={services}
          edges={view.edges}
          anchored={!!view.anchorTitle}
          busy={busy}
          onSelect={onSelect}
          onIndex={(a) => void index(a)}
        />
      )}
    </div>
  );
}

// Roughly what's left below the header, search box and graph chrome. A floor
// keeps a short window from squashing the graph rather than scrolling it.
function availableHeight(): number {
  if (typeof window === "undefined") return 0;
  return Math.max(380, window.innerHeight - 280);
}

function edgeCounts(view: PlatformViewT, alias: string) {
  const out = view.edges.filter((e) => e.from === alias);
  const inc = view.edges.filter((e) => e.to === alias);
  return { out, inc };
}

// The overview as a layered graph. Dependency direction runs left to right,
// so the shape itself is the information — which services are entry points,
// which are shared leaves, where the depth is. A grid of cards with counts
// couldn't show any of that.
function Graph({
  services,
  edges,
  anchored,
  busy,
  onSelect,
  onIndex,
}: {
  services: PlatformService[];
  edges: PlatformEdge[];
  anchored: boolean;
  busy: string | null;
  onSelect: (alias: string) => void;
  onIndex: (alias: string) => void;
}) {
  const [hover, setHover] = useState<string | null>(null);
  // How much height the graph may spread into. Measured from the window
  // rather than the container because the container is sized *by* the graph —
  // reading it back would be circular.
  const [room, setRoom] = useState(() => availableHeight());
  useEffect(() => {
    const onResize = () => setRoom(availableHeight());
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  const l = useMemo(() => layout(services, edges, room), [services, edges, room]);

  // Hovering a service dims everything it isn't connected to. That's the
  // filtering that keeps a large workspace readable without hiding anything.
  const lit = new Set<string>();
  if (hover) {
    lit.add(hover);
    for (const e of l.edges) {
      if (e.edge.from === hover) lit.add(e.edge.to);
      if (e.edge.to === hover) lit.add(e.edge.from);
    }
  }
  // Hover is a transient focus and wins while it's held; otherwise the
  // anchor decides. Both answer the same question — "what is connected to the
  // thing I care about" — so they share the dimming rather than fighting over
  // it.
  const byAlias = new Map(services.map((s) => [s.alias, s]));
  const dim = (alias: string) => {
    if (hover !== null) return !lit.has(alias);
    return anchored && !byAlias.get(alias)?.reachesAnchor;
  };
  const dimEdge = (e: PlatformEdge) => {
    if (hover !== null) return e.from !== hover && e.to !== hover;
    return anchored && !e.reachesAnchor;
  };
  // Only label edges once something has narrowed the picture — hovering a
  // service, or an anchor that lit a path through it.
  const showLabels = hover !== null || anchored;

  return (
    <div className="graph-scroll">
      <div className="graph" style={{ width: l.width, height: l.height }}>
        <svg className="graph-edges" width={l.width} height={l.height} aria-hidden="true">
          {l.edges.map((e) => {
            const faded = dimEdge(e.edge);
            // Counts are shown on demand, not always. Stroke width already
            // carries weight at a glance; printing every number on every edge
            // was clutter you had to read past, and hovering then added more
            // of it. Now hovering is what *reveals* the counts, for the
            // handful of edges you asked about.
            const label = !faded && showLabels && e.edge.calls.length > 1;
            const at = label ? labelPoint(e) : null;
            return (
              <g key={`${e.edge.from}->${e.edge.to}:${e.edge.kind}`}>
                <path
                  d={edgePath(e)}
                  className={`graph-edge${e.back ? " graph-edge--back" : ""}${
                    faded ? " graph-edge--faded" : ""
                  }${!faded && anchored && e.edge.reachesAnchor ? " graph-edge--reaches" : ""}`}
                  // Weight carries how much traffic-by-surface flows along the
                  // edge; one RPC and twenty shouldn't look the same.
                  strokeWidth={Math.min(4, 1 + Math.log2(e.edge.calls.length + 1))}
                />
                {at && (
                  <text className="graph-edge-label" x={at.x} y={at.y + 3}>
                    {e.edge.calls.length}
                  </text>
                )}
              </g>
            );
          })}
        </svg>

        {l.nodes.map((n) => (
          <div
            key={n.service.alias}
            className={`graph-node${n.service.primary ? " graph-node--primary" : ""}${
              n.service.indexed ? "" : " graph-node--unindexed"
            }${dim(n.service.alias) ? " graph-node--dim" : ""}${
            anchored && n.service.reachesAnchor ? " graph-node--reaches" : ""
          }`}
            style={{ left: n.x, top: n.y, width: NODE_W, height: NODE_H }}
            onMouseEnter={() => setHover(n.service.alias)}
            onMouseLeave={() => setHover(null)}
          >
            <button
              type="button"
              className="graph-node-open"
              onClick={() => onSelect(n.service.alias)}
              title={
                n.service.indexed
                  ? n.service.dir
                  : `${n.service.dir} — not indexed, so its outgoing calls are unknown`
              }
            >
              <span className="graph-node-name">{n.service.name}</span>
              {n.service.methods ? (
                <span className="graph-node-meta">{n.service.methods} rpc</span>
              ) : null}
            </button>
            {n.service.error ? (
              <span className="platform-card-error" title={n.service.error}>
                failed
              </span>
            ) : !n.service.indexed ? (
              <button
                type="button"
                className="platform-index"
                onClick={() => onIndex(n.service.alias)}
                disabled={busy === n.service.alias}
                title="read this service's code so its outgoing calls appear"
              >
                {busy === n.service.alias ? "…" : "index"}
              </button>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

// The slice around one service. Drawn as an actual graph because at this
// granularity there are only ever a handful of nodes on screen.
function Slice({
  view,
  alias,
  onSelect,
  onOpenService,
  onOpenSite,
}: {
  view: PlatformViewT;
  alias: string;
  onSelect: (alias: string | null) => void;
  onOpenService: (alias: string) => void;
  onOpenSite: (id: TargetID) => void;
}) {
  const svc = view.services.find((s) => s.alias === alias);
  const { out, inc } = useMemo(() => edgeCounts(view, alias), [view, alias]);
  const nameOf = (a: string) => view.services.find((s) => s.alias === a)?.name ?? a;

  return (
    <div className="slice">
      <div className="slice-bar">
        <button type="button" className="slice-back" onClick={() => onSelect(null)}>
          ‹ all services
        </button>
        <button
          type="button"
          className="slice-open"
          onClick={() => onOpenService(alias)}
          title="open this service's inbound and outbound surface"
        >
          open {nameOf(alias)} ▾
        </button>
        {svc && !svc.indexed && (
          <span className="slice-warn">not indexed — its outgoing calls are unknown</span>
        )}
      </div>

      <div className="slice-columns">
        <EdgeColumn
          side="callers"
          hint="services that call this one"
          edges={inc}
          peer={(e) => e.from}
          nameOf={nameOf}
          onSelect={onSelect}
          onOpenSite={onOpenSite}
          empty="Nothing in the workspace calls it — or the services that do aren't indexed yet."
        />
        <div className="slice-focus">
          <div className="slice-focus-node">{nameOf(alias)}</div>
          {svc?.methods ? <div className="slice-focus-meta">{svc.methods} declared rpc</div> : null}
        </div>
        <EdgeColumn
          side="callees"
          hint="services it calls"
          edges={out}
          peer={(e) => e.to}
          nameOf={nameOf}
          onSelect={onSelect}
          onOpenSite={onOpenSite}
          empty="It calls nothing else in the workspace."
        />
      </div>
    </div>
  );
}

function EdgeColumn({
  side,
  hint,
  edges,
  peer,
  nameOf,
  onSelect,
  onOpenSite,
  empty,
}: {
  side: string;
  hint: string;
  edges: PlatformEdge[];
  peer: (e: PlatformEdge) => string;
  nameOf: (a: string) => string;
  onSelect: (alias: string) => void;
  onOpenSite: (id: TargetID) => void;
  empty: string;
}) {
  return (
    <section className={`slice-col slice-col--${side}`}>
      <div className="slice-col-header">
        <span className="slice-col-title">{side}</span>
        <span className="service-col-count">{edges.length}</span>
      </div>
      <p className="service-col-hint">{hint}</p>
      {edges.length === 0 ? (
        <p className="service-empty">{empty}</p>
      ) : (
        edges.map((e) => (
          <div key={`${e.from}->${e.to}:${e.kind}`} className="slice-edge">
            <button type="button" className="slice-peer" onClick={() => onSelect(peer(e))}>
              {nameOf(peer(e))}
            </button>
            <ul className="slice-calls">
              {e.calls.map((c) => (
                <li key={c.key + (c.site ?? "")}>
                  {/* Every platform-level line stays clickable into real
                      source — the level changes, the destination doesn't. */}
                  <button
                    type="button"
                    className="slice-call"
                    onClick={() => c.site && onOpenSite(c.site)}
                    disabled={!c.site}
                    title={c.siteTitle ? `open ${c.siteTitle}` : c.key}
                  >
                    <span className="slice-call-key">{shortKey(c.key)}</span>
                    {c.siteTitle && <span className="slice-call-site">{c.siteTitle}</span>}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ))
      )}
    </section>
  );
}

// Drop the proto package, as the service view does: the method is what's
// scanned for and truncation would eat it.
function shortKey(key: string): string {
  const [service, method] = key.split("/", 2);
  if (!method) return key;
  return `${service.slice(service.lastIndexOf(".") + 1)}/${method}`;
}
