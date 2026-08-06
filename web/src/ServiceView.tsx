import { useEffect, useState } from "react";
import { fetchServiceView, resolveBinding } from "./api";
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
  anchor,
  repo,
  filters,
  onLoaded,
  onOpen,
}: {
  anchor: TargetID | null;
  // Which workspace service to describe; null means the primary one.
  repo: string | null;
  filters: ServiceFilters;
  // Hands the loaded view up so the sidebar's filter panel can show real
  // facet counts instead of a guess at what's here.
  onLoaded: (v: ServiceViewT | null) => void;
  onOpen: (id: TargetID) => void;
}) {
  const [view, setView] = useState<ServiceViewT | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Bumped after the proto root changes, to refetch the declared surface.
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let alive = true;
    setError(null);
    fetchServiceView(anchor, repo)
      .then((v) => {
        if (!alive) return;
        setView(v);
        onLoaded(v);
      })
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
    // onLoaded is a stable setter from App; including it would refetch on
    // every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [anchor, repo, revision]);

  if (error) return <div className="app-error">{error}</div>;
  if (!view) return <div className="app-loading">loading service…</div>;

  const anchored = !!view.anchorTitle;
  const reaching = view.inbound.filter((b) => b.reachesAnchor).length;
  const reached = view.outbound.filter((b) => b.reachedByAnchor).length;
  const inbound = view.inbound.filter((b) => bindingMatches(b, filters));
  const outbound = view.outbound.filter((b) => bindingMatches(b, filters));
  const hidden = view.inbound.length + view.outbound.length - inbound.length - outbound.length;

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

      {view.repos && view.repos.length > 1 && (
        <div className="service-repos">
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
        </div>
      )}

      {/* An empty gRPC surface and a misconfigured proto root look identical
          without this, so a missing declared surface says why — and offers
          the fix inline rather than sending you back to the command line. */}
      {(view.warning || view.needsProtoRoot) && (
        <div className="service-warning">
          {view.warning && <div>{view.warning}</div>}
          {view.needsProtoRoot && (
            <ProtoRootPicker
              current={view.protoRoot}
              onChanged={() => setRevision((n) => n + 1)}
            />
          )}
        </div>
      )}

      {hidden > 0 && (
        <div className="service-filtered">
          {hidden} binding{hidden === 1 ? "" : "s"} hidden by filters
        </div>
      )}

      <div className="service-columns">
        <Column
          title="inbound"
          hint="where work enters this service"
          bindings={inbound}
          anchored={anchored}
          onOpen={onOpen}
          groupBy={visibilityOf}
          empty="No inbound surface recognized. Routes are found via net/http registration; other routers need their own recognizer."
        />
        <Column
          title="outbound"
          hint="where this service reaches out"
          bindings={outbound}
          anchored={anchored}
          onOpen={onOpen}
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
  groupBy,
  note,
  empty,
}: {
  title: string;
  hint: string;
  bindings: Binding[];
  anchored: boolean;
  onOpen: (id: TargetID) => void;
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

function BindingRow({
  binding,
  anchored,
  grouped,
  onOpen,
}: {
  binding: Binding;
  anchored: boolean;
  // Rendered under a service header, so the row shows only the method.
  grouped?: boolean;
  onOpen: (id: TargetID) => void;
}) {
  // With an anchor loaded, the entrypoints that reach it are lit and the rest
  // dim. That's what makes a large inbound surface readable: you aren't
  // reading eighty routes, you're seeing the two that concern you in context.
  // Inbound lights what runs the anchor; outbound lights what the anchor
  // runs. Same treatment, opposite direction — dimming only applies to the
  // side that has an answer, so an unanchored column isn't greyed out.
  const lit = binding.role === "inbound" ? binding.reachesAnchor : binding.reachedByAnchor;
  const cls = [
    "service-item",
    anchored && lit ? "service-item--reaches" : "",
    anchored && !lit ? "service-item--dim" : "",
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

  const choices = binding.candidates ?? [];

  // A declared binding with no implementation has nothing to open — render
  // it as text rather than a button that would go nowhere.
  const body = (
    <>
      <span className="service-key" title={binding.key}>
        {grouped ? binding.key.slice(binding.key.indexOf("/") + 1) : displayKey(binding)}
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
      ) : choices.length > 0 ? (
        // Several implementations and no way to tell which: offer them all
        // rather than silently picking or dropping the link.
        <ImplPicker choices={choices} body={body} onOpen={onOpen} />
      ) : (
        <span className="service-item-open service-item-open--dead" title={binding.detail}>
          {body}
        </span>
      )}
      {binding.role === "outbound" && <CrossRepoLink binding={binding} onOpen={onOpen} />}
      {binding.stale && (
        <span
          className="service-badge service-badge--stale"
          title="declared in microservice.yaml but nothing in this repo implements it — the declaration may be out of date"
        >
          stale
        </span>
      )}
      {binding.reachesAnchor && (
        <span className="service-badge service-badge--reaches" title="this entrypoint reaches the anchored frame">
          reaches anchor
        </span>
      )}
      {binding.reachedByAnchor && (
        <span
          className="service-badge service-badge--reaches"
          title="the anchored frame reaches this call — it's one the anchor's code path makes"
        >
          anchor reaches
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

// One RPC with several implementations — a decorator in front of a server,
// typically. The same shape the impl switcher uses at interface call sites:
// the tool never guesses which one you meant.
function ImplPicker({
  choices,
  body,
  onOpen,
}: {
  choices: { targetId: TargetID; label: string }[];
  body: React.ReactNode;
  onOpen: (id: TargetID) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <span className="impl-picker">
      <button
        type="button"
        className="service-item-open"
        onClick={() => setOpen((v) => !v)}
        title={`${choices.length} implementations — pick one`}
      >
        {body}
      </button>
      <button
        type="button"
        className={`impl-toggle${open ? " impl-toggle--open" : ""}`}
        onClick={() => setOpen((v) => !v)}
      >
        {choices.length} impls ▾
      </button>
      {open && (
        <ul className="impl-list">
          {choices.map((c) => (
            <li key={c.targetId}>
              <button type="button" onClick={() => onOpen(c.targetId)}>
                {c.label}
              </button>
            </li>
          ))}
        </ul>
      )}
    </span>
  );
}

// The far end of an outbound edge. Kept as its own action rather than
// replacing the row's click: one end is the call site in this repo, the other
// is the implementation in another, and both are things you want one click
// from the same row.
function CrossRepoLink({
  binding,
  onOpen,
}: {
  binding: Binding;
  onOpen: (id: TargetID) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!binding.servedBy) {
    // Only say "nothing serves this" for keys a workspace could plausibly
    // join. Without a workspace open, silence is honest — we haven't looked.
    return null;
  }

  async function jump() {
    setBusy(true);
    setError(null);
    try {
      const res = await resolveBinding(binding.kind, binding.key);
      if (res.target) onOpen(res.target);
      // Several implementations over there: open the first rather than
      // refusing to navigate at all.
      else if (res.candidates?.length) onOpen(res.candidates[0].targetId);
      else setError(res.note ?? `${res.service} has no linkable implementation`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button
        type="button"
        className="service-cross"
        onClick={() => void jump()}
        disabled={busy}
        title={`open the implementation in ${binding.servedBy}${
          binding.servedByRepo ? "" : ""
        } — indexes that repo on first visit`}
      >
        {busy ? "indexing…" : `→ ${binding.servedBy}`}
      </button>
      {error && <span className="service-cross-error">{error}</span>}
    </>
  );
}

function shortFile(p: string): string {
  return p.split("/").slice(-2).join("/");
}
