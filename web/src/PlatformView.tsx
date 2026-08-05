import { useEffect, useMemo, useState } from "react";
import { fetchPlatformView, indexRepo } from "./api";
import type { PlatformEdge, PlatformService, PlatformView as PlatformViewT, TargetID } from "./types";

// The L0 (platform) zoom level: every service in the workspace and the calls
// between them.
//
// This is where node-link rendering earns its place — the node count is the
// number of services, not the number of routes — but only once something is
// selected. An all-pairs canvas is the hairball the service view deliberately
// avoids, so the default is an overview grid, and picking a service draws the
// slice that concerns it: callers on the left, it in the middle, callees on
// the right. Same two-sided shape as the service view, one granularity up.
export function PlatformView({
  filter,
  selected,
  onSelect,
  onOpenService,
  onOpenSite,
}: {
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
    fetchPlatformView()
      .then((v) => alive && setView(v))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, [revision]);

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

  return (
    <div className="platform">
      <header className="platform-header">
        <h2 className="platform-title">workspace</h2>
        <span className="platform-counts">
          {view.services.length} service{view.services.length === 1 ? "" : "s"} ·{" "}
          {view.edges.length} edge{view.edges.length === 1 ? "" : "s"}
        </span>
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
      ) : (
        <ul className="platform-grid">
          {services.map((s) => (
            <ServiceCard
              key={s.alias}
              service={s}
              view={view}
              busy={busy === s.alias}
              onSelect={() => onSelect(s.alias)}
              onIndex={() => void index(s.alias)}
            />
          ))}
          {services.length === 0 && <li className="platform-empty">No service matches “{filter}”.</li>}
        </ul>
      )}
    </div>
  );
}

function edgeCounts(view: PlatformViewT, alias: string) {
  const out = view.edges.filter((e) => e.from === alias);
  const inc = view.edges.filter((e) => e.to === alias);
  return { out, inc };
}

function ServiceCard({
  service,
  view,
  busy,
  onSelect,
  onIndex,
}: {
  service: PlatformService;
  view: PlatformViewT;
  busy: boolean;
  onSelect: () => void;
  onIndex: () => void;
}) {
  const { out, inc } = edgeCounts(view, service.alias);
  return (
    <li className={`platform-card${service.primary ? " platform-card--primary" : ""}`}>
      <button type="button" className="platform-card-open" onClick={onSelect} title={service.dir}>
        <span className="platform-card-name">{service.name}</span>
        <span className="platform-card-meta">
          {inc.length > 0 && <span title="services that call it">← {inc.length}</span>}
          {out.length > 0 && <span title="services it calls">{out.length} →</span>}
          {service.methods ? <span title="RPCs it declares">{service.methods} rpc</span> : null}
        </span>
      </button>
      {service.error ? (
        <span className="platform-card-error" title={service.error}>
          failed
        </span>
      ) : !service.indexed ? (
        <button
          type="button"
          className="platform-index"
          onClick={onIndex}
          disabled={busy}
          title="read this service's code so its outgoing calls appear"
        >
          {busy ? "indexing…" : "index"}
        </button>
      ) : null}
    </li>
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
