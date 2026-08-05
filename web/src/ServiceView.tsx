import { useEffect, useState } from "react";
import { fetchServiceView } from "./api";
import type { Binding, ServiceView as ServiceViewT, TargetID } from "./types";

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

      <div className="service-columns">
        <Column
          title="inbound"
          hint="where work enters this service"
          bindings={view.inbound}
          anchored={anchored}
          onOpen={onOpen}
          empty="No inbound surface recognized. Routes are found via net/http registration; other routers need their own recognizer."
        />
        <Column
          title="outbound"
          hint="where this service reaches out"
          bindings={view.outbound}
          anchored={anchored}
          onOpen={onOpen}
          empty="No outbound edges recognized. Calls whose URL is built at runtime are skipped rather than guessed."
        />
      </div>
    </div>
  );
}

function Column({
  title,
  hint,
  bindings,
  anchored,
  onOpen,
  empty,
}: {
  title: string;
  hint: string;
  bindings: Binding[];
  anchored: boolean;
  onOpen: (id: TargetID) => void;
  empty: string;
}) {
  // Group by kind so routes, topics and subscriptions read as separate
  // surfaces rather than one undifferentiated list.
  const groups = new Map<string, Binding[]>();
  for (const b of bindings) {
    const list = groups.get(b.kind);
    if (list) list.push(b);
    else groups.set(b.kind, [b]);
  }

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
        [...groups].map(([kind, list]) => (
          <div key={kind} className="service-group">
            <div className="service-group-kind">{kind}</div>
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

  return (
    <li className={cls}>
      <button type="button" className="service-item-open" onClick={() => onOpen(open)} title={openTitle}>
        <span className="service-key">{binding.key}</span>
        <span className="service-item-meta">
          {binding.targetTitle ? (
            <span className="service-handler">{binding.targetTitle}</span>
          ) : (
            <span className="service-handler service-handler--site">
              {binding.siteTitle ?? shortFile(binding.file)}
            </span>
          )}
          <span className="service-loc">
            {shortFile(binding.file)}:{binding.line}
          </span>
        </span>
      </button>
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

function shortFile(p: string): string {
  return p.split("/").slice(-2).join("/");
}
