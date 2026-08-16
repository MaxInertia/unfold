import type { Binding, ServiceView } from "./types";

// What the sidebar is *about* has to follow the zoom level, or "a zoom level,
// not a tab" only holds for half the screen: the main panel would move while
// the sidebar kept showing a call tree for a frame you're no longer looking
// at.
//
// At the frame level it stays the files/calls/callers/notes tabs. Above that
// there is no call tree to show, and the thing you actually need is a way to
// cut down a large surface — so the sidebar becomes the filter panel. That
// also makes filtering one mechanism across both upper levels rather than two
// separate ones bolted onto each view.

export interface ServiceFilters {
  text: string;
  visibility: Record<string, boolean>;
  reachingOnly: boolean;
}

export const emptyServiceFilters: ServiceFilters = {
  text: "",
  visibility: {},
  reachingOnly: false,
};

// A binding survives when it matches every active facet. An empty facet is
// inactive rather than exclusive, so the panel starts showing everything.
export function bindingMatches(b: Binding, f: ServiceFilters): boolean {
  const q = f.text.trim().toLowerCase();
  if (q) {
    const hay = `${b.key} ${b.targetTitle ?? ""} ${b.siteTitle ?? ""} ${b.servedBy ?? ""}`;
    if (!hay.toLowerCase().includes(q)) return false;
  }
  const vis = Object.keys(f.visibility).filter((k) => f.visibility[k]);
  if (vis.length > 0 && (!b.visibility || !f.visibility[b.visibility])) return false;
  // The anchor filter applies to whichever direction the row answers for.
  if (f.reachingOnly) {
    const lit = b.role === "inbound" ? b.reachesAnchor : b.reachedByAnchor;
    if (!lit) return false;
  }
  return true;
}

export function ServiceFilterPanel({
  view,
  filters,
  onChange,
}: {
  view: ServiceView | null;
  filters: ServiceFilters;
  onChange: (f: ServiceFilters) => void;
}) {
  const all = view ? [...view.inbound, ...view.outbound] : [];
  const counts = new Map<string, number>();
  for (const b of all) {
    if (b.visibility) counts.set(b.visibility, (counts.get(b.visibility) ?? 0) + 1);
  }
  const reaching = view?.inbound.filter((b) => b.reachesAnchor).length ?? 0;
  const reachedCount = view?.outbound.filter((b) => b.reachedByAnchor).length ?? 0;
  const set = (patch: Partial<ServiceFilters>) => onChange({ ...filters, ...patch });

  return (
    <div className="filters">
      <input
        type="text"
        className="filters-text"
        placeholder="filter by key, handler, service…"
        value={filters.text}
        onChange={(e) => set({ text: e.target.value })}
        spellCheck={false}
      />

      {counts.size > 0 && (
        <fieldset className="filters-group">
          <legend>reach</legend>
          {["public", "platform", "internal"].map((v) =>
            counts.has(v) ? (
              <label key={v} className="filters-row">
                <input
                  type="checkbox"
                  checked={!!filters.visibility[v]}
                  onChange={(e) =>
                    set({ visibility: { ...filters.visibility, [v]: e.target.checked } })
                  }
                />
                <span>{v}</span>
                <span className="filters-count">{counts.get(v)}</span>
              </label>
            ) : null,
          )}
        </fieldset>
      )}

      {view?.anchorTitle && (
        <fieldset className="filters-group">
          <legend>anchor</legend>
          <label className="filters-row">
            <input
              type="checkbox"
              checked={filters.reachingOnly}
              onChange={(e) => set({ reachingOnly: e.target.checked })}
            />
            <span>only what connects to {view.anchorTitle}</span>
            <span className="filters-count">{reaching + reachedCount}</span>
          </label>
        </fieldset>
      )}

      {(filters.text || filters.reachingOnly || Object.values(filters.visibility).some(Boolean)) && (
        <button type="button" className="filters-clear" onClick={() => onChange(emptyServiceFilters)}>
          clear filters
        </button>
      )}
    </div>
  );
}

// The platform level's filter. What it filters depends on which reading is
// showing, so the copy follows the mode rather than naming services while the
// list under it is APIs.
export function PlatformFilterPanel({
  text,
  onChange,
  mode = "graph",
}: {
  text: string;
  onChange: (text: string) => void;
  mode?: "graph" | "calls" | "keys";
}) {
  const calls = mode === "calls";
  return (
    <div className="filters">
      <input
        type="text"
        className="filters-text"
        placeholder={calls ? "filter APIs…" : "filter services…"}
        value={text}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
      />
      <p className="filters-hint">
        {calls
          ? "Click an API to focus the graph on its chain — everything that reaches it, and everything it reaches."
          : "Pick a service to see the slice around it — who calls it, and what it calls."}
      </p>
    </div>
  );
}
