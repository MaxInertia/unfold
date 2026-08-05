import { useEffect, useState } from "react";
import { fetchServiceView } from "./api";
import type { Binding, BindingVisibility, ServiceView as ServiceViewT, TargetID } from "./types";

// The L1 (service) zoom level: what enters this service on the left, what it
// reaches out to on the right.
//
// Deliberately *not* a node-link graph. A service with eighty routes and
// thirty dependencies force-directed onto a canvas is a hairball — the
// standard failure mode of tools in this space. A two-sided card stays
// readable at that size, and node-link rendering is saved for the platform
// level where the node count is the number of services.
export function ServiceView({
  anchor,
  onOpen,
}: {
  anchor: TargetID | null;
  onOpen: (id: TargetID) => void;
}) {
  const [view, setView] = useState<ServiceViewT | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setError(null);
    fetchServiceView(anchor)
      .then((v) => alive && setView(v))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, [anchor]);

  if (error) return <div className="app-error">{error}</div>;
  if (!view) return <div className="app-loading">loading service…</div>;

  const anchored = !!view.anchorTitle;
  const reaching = view.inbound.filter((b) => b.reachesAnchor).length;

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
          </span>
        )}
      </header>

      {/* An empty gRPC surface and a misconfigured proto root look identical
          without this, so a missing declared surface says why. */}
      {view.warning && <div className="service-warning">{view.warning}</div>}

      <div className="service-columns">
        <Column
          title="inbound"
          hint="where work enters this service"
          bindings={view.inbound}
          anchored={anchored}
          onOpen={onOpen}
          groupBy={visibilityOf}
          empty="No inbound surface recognized. Routes are found via net/http registration; other routers need their own recognizer."
        />
        <Column
          title="outbound"
          hint="where this service reaches out"
          bindings={view.outbound}
          anchored={anchored}
          onOpen={onOpen}
          groupBy={(b) => b.kind}
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
  groupBy,
  empty,
}: {
  title: string;
  hint: string;
  bindings: Binding[];
  anchored: boolean;
  onOpen: (id: TargetID) => void;
  groupBy: (b: Binding) => string;
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
            <ul className="service-list">
              {list.map((b, i) => (
                <BindingRow
                  key={`${b.kind}:${b.key}:${b.file}:${b.line}:${i}`}
                  binding={b}
                  anchored={anchored}
                  onOpen={onOpen}
                />
              ))}
            </ul>
          </div>
        ))
      )}
    </section>
  );
}

function BindingRow({
  binding,
  anchored,
  onOpen,
}: {
  binding: Binding;
  anchored: boolean;
  onOpen: (id: TargetID) => void;
}) {
  // With an anchor loaded, the entrypoints that reach it are lit and the rest
  // dim. That's what makes a large inbound surface readable: you aren't
  // reading eighty routes, you're seeing the two that concern you in context.
  const cls = [
    "service-item",
    anchored && binding.reachesAnchor ? "service-item--reaches" : "",
    anchored && !binding.reachesAnchor ? "service-item--dim" : "",
  ]
    .filter(Boolean)
    .join(" ");

  // Prefer opening the handler; fall back to the registration site so every
  // row lands in source even when the far end isn't in this index.
  const open = binding.target || binding.site;
  // Provenance goes in the tooltip rather than the row: "ServeMux.HandleFunc"
  // repeated down twelve rows is noise, but it's the first thing you want
  // when a binding looks wrong.
  const openTitle = [
    binding.target
      ? `open ${binding.targetTitle ?? "handler"}`
      : `open ${binding.siteTitle ?? "registration site"}`,
    binding.detail && `recognized from ${binding.detail}`,
  ]
    .filter(Boolean)
    .join(" · ");

  // A declared binding with no implementation has nothing to open — render
  // it as text rather than a button that would go nowhere.
  const body = (
    <>
      <span className="service-key" title={binding.key}>
        {displayKey(binding)}
      </span>
      <span className="service-item-meta">
        {binding.targetTitle ? (
          <span className="service-handler">{binding.targetTitle}</span>
        ) : binding.siteTitle ? (
          <span className="service-handler service-handler--site">{binding.siteTitle}</span>
        ) : null}
        {binding.file && (
          <span className="service-loc">
            {shortFile(binding.file)}
            {binding.line ? `:${binding.line}` : ""}
          </span>
        )}
      </span>
    </>
  );

  return (
    <li className={cls}>
      {open ? (
        <button type="button" className="service-item-open" onClick={() => onOpen(open)} title={openTitle}>
          {body}
        </button>
      ) : (
        <span className="service-item-open service-item-open--dead" title={binding.detail}>
          {body}
        </span>
      )}
      {binding.stale && (
        <span
          className="service-badge service-badge--stale"
          title="declared in microservice.yaml but not implemented in code — the declaration may be out of date"
        >
          stale
        </span>
      )}
      {binding.reachesAnchor && (
        <span className="service-badge service-badge--reaches" title="this entrypoint reaches the anchored frame">
          reaches anchor
        </span>
      )}
      {/* Only non-exact resolution is labelled — an exact literal↔literal
          match is the baseline and doesn't need a badge. */}
      {binding.confidence && binding.confidence !== "exact" && (
        <span
          className={`service-badge service-badge--${binding.confidence}`}
          title={
            binding.confidence === "inferred"
              ? `heuristic: the key is literal but the direction is a guess (${binding.detail ?? ""})`
              : "declared by a manifest or infra-as-code"
          }
        >
          {binding.confidence}
        </span>
      )}
    </li>
  );
}

// A gRPC key is "<proto package>.<Service>/<Method>". The package is the
// least interesting part and the longest, and truncation would eat the method
// name — which is exactly what you scan the column for. Drop the package for
// display; the full key stays in the row's tooltip.
function displayKey(b: Binding): string {
  if (b.kind !== "grpc.method") return b.key;
  const [service, method] = b.key.split("/", 2);
  if (!method) return b.key;
  const bare = service.slice(service.lastIndexOf(".") + 1);
  return `${bare}/${method}`;
}

function shortFile(p: string): string {
  return p.split("/").slice(-2).join("/");
}
