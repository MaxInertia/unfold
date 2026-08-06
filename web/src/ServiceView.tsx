import { useEffect, useMemo, useState } from "react";
import { BindingRow, displayKey, type CrossingState } from "./BindingRow";
import { buildCrossing, crossingSummary } from "./crossing";
import { LinkRepo } from "./LinkRepo";
import { ProtoRootPicker } from "./ProtoRootPicker";
import type { Binding, BindingVisibility, ServiceView as ServiceViewT, TargetID } from "./types";
import { bindingMatches, type ServiceFilters } from "./ZoomSidebar";

// The L1 (service) zoom level: what enters this service on the left, what it
// reaches out to on the right.
//
// Deliberately *not* a node-link graph. A service with eighty routes and
// thirty dependencies force-directed onto a canvas is a hairball — the
// standard failure mode of tools in this space. A two-sided card stays
// readable at that size, and node-link rendering is saved for the platform
// level where the node count is the number of services.
export function ServiceView({
  view,
  filters,
  onProtoRootChanged,
  onOpen,
}: {
  // Fetched by App rather than here. The same response now feeds the trail,
  // the sidebar's facet counts, and the frame-level anchor panels, so one
  // owner requesting it once beats four components each asking for their own
  // copy and disagreeing about which is current.
  view: ServiceViewT | null;
  filters: ServiceFilters;
  onProtoRootChanged: () => void;
  onOpen: (id: TargetID) => void;
}) {
  // Which binding is being traced across the two columns, by binding id.
  const [traced, setTraced] = useState<string | null>(null);
  const crossing = useMemo(() => buildCrossing(view), [view]);
  // Ids are positions in a surface, so they mean nothing once the surface is
  // rebuilt — a reindex or a new proto root would otherwise leave the
  // selection pointing at whatever binding inherited the number.
  useEffect(() => setTraced(null), [view]);

  if (!view) return <div className="app-loading">loading service…</div>;

  const anchored = !!view.anchorTitle;
  const reaching = view.inbound.filter((b) => b.reachesAnchor).length;
  const reached = view.outbound.filter((b) => b.reachedByAnchor).length;
  const inbound = view.inbound.filter((b) => bindingMatches(b, filters));
  const outbound = view.outbound.filter((b) => bindingMatches(b, filters));
  const hidden = view.inbound.length + view.outbound.length - inbound.length - outbound.length;

  const all = [...view.inbound, ...view.outbound];
  const tracedBinding = traced ? all.find((b) => b.id === traced) : undefined;
  const linked = traced ? crossing.linked(traced) : new Set<string>();
  const crossingFor = (b: Binding): CrossingState | undefined =>
    b.id
      ? {
          active: !!tracedBinding,
          selected: b.id === traced,
          linked: linked.has(b.id),
          onSelect: () => setTraced((cur) => (cur === b.id ? null : b.id!)),
        }
      : undefined;

  return (
    <div className="service">
      <header className="service-header">
        <h2 className="service-name">{view.name}</h2>
        {view.module && <code className="service-module">{view.module}</code>}
        {anchored && (
          <span className="service-anchor" title="the frame you zoomed out from">
            anchored on <b>{view.anchorTitle}</b>
            {" · "}
            {reaching === 0
              ? "no entrypoint reaches it"
              : `${reaching} entrypoint${reaching === 1 ? "" : "s"} reach it`}
            {reached > 0 && ` · it makes ${reached} call${reached === 1 ? "" : "s"}`}
          </span>
        )}
      </header>

      {/* The strip renders even with nothing to list, because a single-repo
          session is exactly the one where you need to open another repo — and
          hiding the control until a workspace already exists would mean you
          could only add a second repo once you had two. */}
      <div className="service-repos">
        {view.repos && view.repos.length > 1 && (
          <>
            <span className="service-repos-label">workspace</span>
            {view.repos.map((r) => (
            <span
              key={r.alias}
              // "primary" is the repo unfold was launched in; "current" is
              // the one this view is about. They differ as soon as you pick
              // another service at the platform level, and marking only the
              // former would point at the wrong card.
              className={`service-repo${r.name === view.name ? " service-repo--current" : ""}${
                r.primary ? " service-repo--primary" : ""
              }${r.indexed ? " service-repo--indexed" : ""}`}
              title={
                r.error
                  ? `${r.dir} — ${r.error}`
                  : r.indexed
                    ? `${r.dir} — indexed`
                    : `${r.dir} — not indexed yet; opening something here will index it`
              }
            >
                {r.name}
              </span>
            ))}
          </>
        )}
        <LinkRepo />
      </div>

      {/* An empty gRPC surface and a misconfigured proto root look identical
          without this, so a missing declared surface says why — and offers
          the fix inline rather than sending you back to the command line. */}
      {(view.warning || view.needsProtoRoot) && (
        <div className="service-warning">
          {view.warning && <div>{view.warning}</div>}
          {view.needsProtoRoot && (
            <ProtoRootPicker current={view.protoRoot} onChanged={onProtoRootChanged} />
          )}
        </div>
      )}

      {hidden > 0 && (
        <div className="service-filtered">
          {hidden} binding{hidden === 1 ? "" : "s"} hidden by filters
        </div>
      )}

      {/* What's being traced, stated in words. The dimming shows *which* rows
          connect; only this can say that the answer is zero, or that there
          isn't one — both of which look identical as "nothing is lit". */}
      {tracedBinding && (
        <div className="service-trace">
          <span className="service-trace-key">{displayKey(tracedBinding)}</span>
          <span className="service-trace-summary">
            {crossingSummary(
              tracedBinding,
              linked.size,
              crossing.known(tracedBinding),
            )}
          </span>
          <button type="button" className="service-trace-clear" onClick={() => setTraced(null)}>
            clear
          </button>
        </div>
      )}

      <div className="service-columns">
        <Column
          title="inbound"
          hint="where work enters this service"
          bindings={inbound}
          anchored={anchored}
          onOpen={onOpen}
          crossingFor={crossingFor}
          groupBy={visibilityOf}
          empty="No inbound surface recognized. Routes are found via net/http registration; other routers need their own recognizer."
        />
        <Column
          title="outbound"
          hint="where this service reaches out"
          bindings={outbound}
          anchored={anchored}
          onOpen={onOpen}
          crossingFor={crossingFor}
          groupBy={(b) => b.kind}
          note={
            view.outboundUnreachable
              ? `${view.outboundUnreachable} call site${
                  view.outboundUnreachable === 1 ? "" : "s"
                } hidden — nothing reaches them from a recognized entrypoint`
              : undefined
          }
          empty="No outbound edges recognized. Calls whose URL is built at runtime are skipped rather than guessed."
        />
      </div>
    </div>
  );
}

// Inbound is grouped by reach rather than by kind: what you want to know
// about an entrypoint first is who can get to it, not which library
// registered it. Ordered outermost-in, so the public surface reads first.
const VISIBILITY_ORDER: BindingVisibility[] = ["public", "platform", "internal"];

const VISIBILITY_HINT: Record<BindingVisibility, string> = {
  public: "reachable from outside the platform — declared in microservice.yaml",
  platform: "reachable by other services — proto-declared, in the generated SDK",
  internal: "registered here but named by neither publicRoutes nor an SDK proto",
};

function visibilityOf(b: Binding): string {
  return b.visibility ?? b.kind;
}

function Column({
  title,
  hint,
  bindings,
  anchored,
  onOpen,
  crossingFor,
  groupBy,
  note,
  empty,
}: {
  title: string;
  hint: string;
  bindings: Binding[];
  anchored: boolean;
  onOpen: (id: TargetID) => void;
  crossingFor: (b: Binding) => CrossingState | undefined;
  groupBy: (b: Binding) => string;
  // Says what was left out, so an empty or short column is never unexplained.
  note?: string;
  empty: string;
}) {
  const groups = new Map<string, Binding[]>();
  for (const b of bindings) {
    const key = groupBy(b);
    const list = groups.get(key);
    if (list) list.push(b);
    else groups.set(key, [b]);
  }
  // Visibility groups get a fixed outermost-in order; anything else keeps
  // insertion order (which the backend already sorts).
  const ordered = [...groups].sort((a, b) => {
    const ia = VISIBILITY_ORDER.indexOf(a[0] as BindingVisibility);
    const ib = VISIBILITY_ORDER.indexOf(b[0] as BindingVisibility);
    if (ia === -1 && ib === -1) return 0;
    return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
  });

  return (
    <section className={`service-col service-col--${title}`}>
      <div className="service-col-header">
        <span className="service-col-title">{title}</span>
        <span className="service-col-count">{bindings.length}</span>
      </div>
      <p className="service-col-hint">{hint}</p>
      {note && <p className="service-col-note">{note}</p>}
      {bindings.length === 0 ? (
        <p className="service-empty">{empty}</p>
      ) : (
        ordered.map(([group, list]) => (
          <div key={group} className="service-group">
            <div
              className="service-group-kind"
              title={VISIBILITY_HINT[group as BindingVisibility]}
            >
              {group}
            </div>
            {/* RPCs belong to a service, and reading them as a flat list of
                fully-qualified names buries that: the package prefix repeats
                on every row while the thing that differs — the method — sits
                at the far end where truncation eats it. Grouped, the service
                is stated once and the rows are just its methods. */}
            {subgroups(list).map(([service, rows]) => (
              <div key={service} className={service ? "rpc-group" : ""}>
                {service && (
                  <div className="rpc-group-service" title={service}>
                    {bareService(service)}
                  </div>
                )}
                <ul className="service-list">
                  {rows.map((b, i) => (
                    <BindingRow
                      key={`${b.kind}:${b.key}:${b.file}:${b.line}:${i}`}
                      binding={b}
                      anchored={anchored}
                      grouped={!!service}
                      crossing={crossingFor(b)}
                      onOpen={onOpen}
                    />
                  ))}
                </ul>
              </div>
            ))}
          </div>
        ))
      )}
    </section>
  );
}

// subgroups splits a column group so gRPC methods cluster under their
// service. Everything else stays in one flat bucket, keyed "", which renders
// without a header.
function subgroups(list: Binding[]): [string, Binding[]][] {
  const m = new Map<string, Binding[]>();
  for (const b of list) {
    const k = b.kind === "grpc.method" ? grpcService(b.key) : "";
    const cur = m.get(k);
    if (cur) cur.push(b);
    else m.set(k, [b]);
  }
  // Flat rows first, then services alphabetically by their bare name.
  return [...m].sort((a, b) => {
    if (!a[0]) return -1;
    if (!b[0]) return 1;
    return bareService(a[0]).localeCompare(bareService(b[0]));
  });
}

function grpcService(key: string): string {
  const i = key.indexOf("/");
  return i > 0 ? key.slice(0, i) : "";
}

function bareService(fq: string): string {
  return fq.slice(fq.lastIndexOf(".") + 1);
}
