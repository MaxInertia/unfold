import { useState } from "react";
import { resolveBinding } from "./api";
import type { Binding, TargetID } from "./types";

// One row of a service surface. Lives on its own because the same row now
// renders in three places — the L1 inbound and outbound columns, and the
// frame-level entrypoints/outbounds panels. They are the *same* data asked
// about from different altitudes, so they have to look and behave the same;
// a second hand-written renderer would drift the moment either grew a badge.

export function BindingRow({
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
export function displayKey(b: Binding): string {
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
        title={`open the implementation in ${binding.servedBy} — indexes that repo on first visit`}
      >
        {busy ? "indexing…" : `→ ${binding.servedBy}`}
      </button>
      {error && <span className="service-cross-error">{error}</span>}
    </>
  );
}

export function shortFile(p: string): string {
  return p.split("/").slice(-2).join("/");
}
