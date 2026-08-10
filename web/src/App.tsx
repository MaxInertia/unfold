import { useEffect, useState, type ReactNode } from "react";
import { Frame } from "./Frame";
import { CallTree } from "./CallTree";
import { CallersTree } from "./CallersTree";
import { FileTree } from "./FileTree";
import { StickyHeaders } from "./StickyHeaders";
import { SettingsPanel } from "./SettingsPanel";
import { RulesPanel } from "./RulesPanel";
import { useSettings } from "./settings";
import { NotesList } from "./NotesUI";
import { loadNotes } from "./notes";
import { ServiceView } from "./ServiceView";
import { PlatformView } from "./PlatformView";
import { ChannelIndex } from "./ChannelIndex";
import { WorkspaceStatus } from "./WorkspaceStatus";
import { EntrypointsPanel, OutboundsPanel } from "./AnchorPanels";
import { ZoomTrail } from "./ZoomTrail";
import {
  emptyServiceFilters,
  PlatformFilterPanel,
  ServiceFilterPanel,
  type ServiceFilters,
} from "./ZoomSidebar";
import { repoOf, zoomIn, zoomOut } from "./zoom";
import { matches } from "./keybindings";
import { fetchServiceView, fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult, ServiceView as ServiceViewT } from "./types";
import { ViewStoreProvider, useViewStore } from "./viewState";
import { ReloadProvider, useReloadRevision } from "./reload";
import { setBookmarkProject, useBookmarks } from "./bookmarks";
import { closeRules, toggleRules, useRulesPanel } from "./rules";

const TREE_COLLAPSED_KEY = "unfold.tree.collapsed";
const SIDEBAR_WIDTH_KEY = "unfold.sidebar.width";
const RIGHT_COLLAPSED_KEY = "unfold.right.collapsed";
const RIGHT_WIDTH_KEY = "unfold.right.width";
const SIDEBAR_MIN = 200;

export function App() {
  return (
    <ViewStoreProvider>
      <ReloadProvider>
        <AppShell />
      </ReloadProvider>
    </ViewStoreProvider>
  );
}

function AppShell() {
  const store = useViewStore();
  const symbol = store.symbol;
  const revision = useReloadRevision();
  const [target, setTarget] = useState<string | null>(null);
  const [rootFrame, setRootFrame] = useState<FrameT | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [treeCollapsed, setTreeCollapsed] = useState(
    () => localStorage.getItem(TREE_COLLAPSED_KEY) === "1",
  );
  // The left panel is about what leads *to* this frame — its entrypoints, its
  // callers, the files it lives among. The call tree is the opposite direction
  // and lives on the right with the outbounds, beside the code it describes
  // rather than across it.
  //
  // Both tabs start unpicked rather than at a fixed name, because the right
  // default depends on something that isn't known at mount: whether this
  // engine has a recognized surface. With one, each side opens on the
  // platform-level answer — which entrypoints reach this code, and what it
  // calls out to. Without one those tabs don't exist, so the fallback is the
  // nearest thing that does: callers on the left, the call tree on the right.
  // A pick is remembered as itself, so choosing a tab survives health
  // resolving late and never gets overwritten by a default.
  const [sidebarTab, setSidebarTab] = useState<
    "files" | "callers" | "entrypoints" | "notes" | null
  >(null);
  const [rightTab, setRightTab] = useState<"calls" | "outbounds" | null>(null);
  const [reindexed, setReindexed] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  // The recognizers panel is opened from two places — this button, and a
  // "recognized by" chip on a hover card deep inside a frame — so its open
  // state lives in a store rather than here.
  const { open: rulesOpen } = useRulesPanel();
  const settings = useSettings();
  // Zoom is a level, not a tab: the frame tree stays mounted in the view
  // store either way, so switching levels never costs expansion state. It
  // lives in the store (and so in the URL) rather than in local state, which
  // is what makes a zoomed-out view shareable and back/forward-able.
  const setZoom = store.setZoom;
  const [platform, setPlatform] = useState(false);
  const [workspace, setWorkspace] = useState(false);
  // Now that the level comes from the URL it can name a rung this session
  // doesn't have — a hand-edited hash, or a link shared from a workspace into
  // a plain repo. Fall back to the nearest level that exists rather than
  // rendering a view whose data can't be fetched. The store keeps what it was
  // asked for, so the level returns on its own if the missing capability
  // shows up (health resolves, a workspace opens).
  const zoom = !platform
    ? "frame"
    : store.zoom === "platform" && !workspace
      ? "service"
      : store.zoom;
  // The service surface for whatever is anchored. One owner, one request:
  // the trail, the sidebar's facet counts, the L1 columns, and both anchor
  // panels are all views onto this single response.
  const [serviceView, setServiceView] = useState<ServiceViewT | null>(null);
  // Bumped when the proto root changes, to refetch the declared surface.
  const [protoRevision, setProtoRevision] = useState(0);
  // Filters live here, not inside the views, because the sidebar owns them
  // once you're above the frame level.
  const [serviceFilters, setServiceFilters] = useState<ServiceFilters>(emptyServiceFilters);
  const [platformFilter, setPlatformFilter] = useState("");
  // Which reading of the platform level is showing. Local rather than in the
  // URL: it is a way of looking at one level, not a different place to be.
  const [platformMode, setPlatformMode] = useState<"graph" | "keys">("graph");
  const selectedService = store.service;
  const setSelectedService = store.setService;
  const [sidebarWidth, setSidebarWidth] = useState(() => {
    const v = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return v >= SIDEBAR_MIN ? v : 280;
  });
  // Whether the right panel starts open follows the same question as its
  // default tab: with a recognized surface it opens on outbounds, which is
  // worth the horizontal space unprompted. Without one it is the call tree
  // alone — useful, but a mirror of what you already did — so it stays a
  // choice, as it was before the tree moved here. null means "never chosen";
  // an explicit toggle is stored and outranks the default forever after.
  const [rightCollapsed, setRightCollapsed] = useState<boolean | null>(() => {
    const v = localStorage.getItem(RIGHT_COLLAPSED_KEY);
    return v === null ? null : v === "1";
  });
  const [rightWidth, setRightWidth] = useState(() => {
    const v = Number(localStorage.getItem(RIGHT_WIDTH_KEY));
    return v >= SIDEBAR_MIN ? v : 280;
  });

  // What each panel actually shows: a pick if there is one, the
  // platform-dependent default otherwise.
  const leftTab = sidebarTab ?? (platform ? "entrypoints" : "callers");
  const rightPanelTab = rightTab ?? (platform ? "outbounds" : "calls");
  const rightIsCollapsed = rightCollapsed ?? !platform;

  // Which service the views are about. Zooming out follows the code you were
  // reading — the frame's own repo — rather than snapping back to the one
  // unfold was launched in; an explicit pick at the platform level overrides
  // that until you descend into code again.
  const serviceRepo = selectedService ?? repoOf(rootFrame?.id);

  // Flash a brief toast whenever watch mode reindexes (revision > 0).
  useEffect(() => {
    if (revision === 0) return;
    setReindexed(true);
    const t = setTimeout(() => setReindexed(false), 1800);
    return () => clearTimeout(t);
  }, [revision]);

  useEffect(() => {
    localStorage.setItem(TREE_COLLAPSED_KEY, treeCollapsed ? "1" : "0");
  }, [treeCollapsed]);

  useEffect(() => {
    localStorage.setItem(SIDEBAR_WIDTH_KEY, String(sidebarWidth));
  }, [sidebarWidth]);

  // Only a real toggle is written. Persisting the derived value would freeze
  // whatever this session happened to open as, so a repo opened once without a
  // platform surface would keep the panel shut in every repo after it.
  useEffect(() => {
    if (rightCollapsed === null) return;
    localStorage.setItem(RIGHT_COLLAPSED_KEY, rightCollapsed ? "1" : "0");
  }, [rightCollapsed]);

  useEffect(() => {
    localStorage.setItem(RIGHT_WIDTH_KEY, String(rightWidth));
  }, [rightWidth]);

  // Drag a panel's inner edge to resize it. The right panel grows the other
  // way, so its delta is inverted — otherwise dragging left would shrink the
  // thing you're pulling toward you.
  function onResizeStart(e: React.PointerEvent, side: "left" | "right" = "left") {
    e.preventDefault();
    const startX = e.clientX;
    const startW = side === "left" ? sidebarWidth : rightWidth;
    const setter = side === "left" ? setSidebarWidth : setRightWidth;
    const sign = side === "left" ? 1 : -1;
    const max = Math.max(280, Math.floor(window.innerWidth * 0.6));
    const move = (ev: PointerEvent) => {
      setter(Math.min(max, Math.max(SIDEBAR_MIN, startW + sign * (ev.clientX - startX))));
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      document.body.classList.remove("resizing");
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    document.body.classList.add("resizing");
  }

  useEffect(() => {
    if (!symbol) {
      setRootFrame(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    fetchSymbol(symbol)
      .then((f) => {
        setRootFrame(f);
        setLoading(false);
      })
      .catch((e: Error) => {
        setError(e.message);
        setLoading(false);
      });
    // `revision` re-runs this after a watch-mode reindex so the root frame
    // (and, via the remount below, its expanded children) refetch.
  }, [symbol, revision]);

  // Health is what tells the UI a platform and a workspace exist, so a single
  // dropped response cost the whole page its zoom levels: it was fetched once,
  // the failure was swallowed, and the only cure was a manual reload. Retry
  // until it answers — there is no correct view without it — and refetch on
  // `revision`, because linking a repo turns a single-repo session into a
  // workspace and the platform level has to appear without a refresh.
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const attempt = (tries: number) => {
      fetch("/api/health")
        .then((r) => {
          if (!r.ok) throw new Error(`health: ${r.status}`);
          return r.json();
        })
        .then((h) => {
          if (!alive) return;
          setTarget(h.target ?? null);
          setBookmarkProject(h.target ?? null); // namespace bookmarks per project
          setPlatform(!!h.platform);
          setWorkspace(!!h.workspace);
        })
        .catch(() => {
          if (!alive) return;
          // Backs off to a second and stays there: the server is local, so it
          // is either still coming up or gone, and a page left open across a
          // restart should recover on its own rather than wait to be reloaded.
          timer = setTimeout(() => attempt(tries + 1), Math.min(1000, 100 * 2 ** tries));
        });
    };
    attempt(0);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [revision]);

  useEffect(() => {
    loadNotes();
  }, []);

  // Everything the trail shows comes from one request: the service's name,
  // whether the current frame is even *in* that service, and how many
  // entrypoints reach it. The backend only echoes an anchor that belongs to
  // the service being described, so viewing another service correctly drops
  // the anchor tail instead of implying a frame it doesn't contain.
  useEffect(() => {
    if (!platform) return;
    let alive = true;
    fetchServiceView(rootFrame?.id ?? null, serviceRepo)
      .then((v) => alive && setServiceView(v))
      .catch(() => alive && setServiceView(null));
    return () => {
      alive = false;
    };
  }, [platform, rootFrame?.id, serviceRepo, revision, protoRevision]);

  const serviceName = serviceView?.name ?? null;
  const trailAnchor = serviceView?.anchorTitle ?? null;
  const entrypointCount = serviceView?.anchor
    ? serviceView.inbound.filter((b) => b.reachesAnchor).length
    : null;
  // How many outbound calls the anchor's code path makes — the right panel's
  // badge, and the reason to open it at all.
  const outboundCount = serviceView?.anchor
    ? serviceView.outbound.filter((b) => b.reachedByAnchor).length
    : null;

  // Keyboard zoom and history. The chords live in the keybinding registry,
  // which is also what the settings panel lists — so the documented shortcut
  // and the wired one can't disagree.
  //
  // History isn't gated on platform mode: re-rooting through a caller is a
  // lateral move worth undoing in a single repo too.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (matches("history.back", e)) {
        e.preventDefault();
        history.back();
      } else if (matches("history.forward", e)) {
        e.preventDefault();
        history.forward();
      } else if (platform && matches("zoom.out", e)) {
        e.preventDefault();
        setZoom(zoomOut(zoom, workspace));
      } else if (platform && matches("zoom.in", e)) {
        e.preventDefault();
        setZoom(zoomIn(zoom, workspace));
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [platform, workspace, zoom, setZoom]);



  return (
    <div
      className={`app${treeCollapsed ? " app--tree-collapsed" : ""}${
        settings.indentMode === "indent" ? " app--indent" : ""
      }`}
    >
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
        {/* One group, laid out rather than each button pinned to the right
            edge on its own — which is what they were, so they sat on top of
            each other whenever more than one was showing. */}
        <div className="app-actions">
          {platform && (
            <button
              type="button"
              className={`app-settings${rulesOpen ? " app-settings--open" : ""}`}
              onClick={toggleRules}
              title="recognizers — what unfold treats as a platform edge, and how much each rule matched"
              aria-label="toggle recognizers"
            >
              ⌥
            </button>
          )}
          {/* Beside settings, because it is the same kind of thing: not part
              of the code you are reading, but something about the session you
              occasionally need to see. */}
          <WorkspaceStatus />
          <button
            type="button"
            className={`app-settings${settingsOpen ? " app-settings--open" : ""}`}
            onClick={() => setSettingsOpen((v) => !v)}
            title="settings"
            aria-label="toggle settings"
          >
            ⚙
          </button>
        </div>
      </header>
      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} />}
      {rulesOpen && <RulesPanel onClose={closeRules} />}
      {reindexed && <div className="app-toast">reindexed · view refreshed</div>}
      <div className="app-main">
        <aside
          className={`tree-panel${treeCollapsed ? " tree-panel--collapsed" : ""}`}
          style={treeCollapsed ? undefined : { flex: `0 0 ${sidebarWidth}px` }}
        >
          {treeCollapsed ? (
            <button
              type="button"
              className="tree-expand"
              onClick={() => setTreeCollapsed(false)}
              title="show sidebar"
              aria-label="show sidebar"
            >
              <span className="tree-expand-icon">›</span>
              <span className="tree-expand-label">tree · marks</span>
            </button>
          ) : (
            <>
              <BookmarksPanel
                anchor={rootFrame?.id ?? null}
                onOpen={(id) => store.setSymbol(id)}
              />
              <div className="tree-inner">
                {/* The sidebar follows the zoom level. Above the frame there
                    is no call tree to show, and what you need instead is a
                    way to cut a large surface down — so the tabs give way to
                    the filter panel rather than sitting there describing a
                    frame you're no longer looking at. */}
                <div className="tree-header tree-tabs">
                  {zoom === "frame" ? (
                    <>
                      {/* Ordered by how far each answer is from the code:
                          entrypoints is the whole reason this function runs,
                          callers is the hop below it, and files is where the
                          text happens to sit. That also puts the default
                          first and its fallback second.

                          Entrypoints is only offered in platform mode, because
                          without a recognized surface the tab would have
                          nothing to show and no honest way to say why. */}
                      {platform && (
                        <button
                          type="button"
                          className={`tree-tab${
                            leftTab === "entrypoints" ? " tree-tab--active" : ""
                          }`}
                          onClick={() => setSidebarTab("entrypoints")}
                          title="the routes, RPCs and subscriptions that reach this frame — why this code runs at all"
                        >
                          entrypoints
                          {entrypointCount !== null && entrypointCount > 0 && (
                            <span className="tree-tab-count">{entrypointCount}</span>
                          )}
                        </button>
                      )}
                      <button
                        type="button"
                        className={`tree-tab${leftTab === "callers" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("callers")}
                        title="who calls the focused function — expand to walk toward entry points"
                      >
                        callers
                      </button>
                      <button
                        type="button"
                        className={`tree-tab${leftTab === "files" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("files")}
                      >
                        files
                      </button>
                      <button
                        type="button"
                        className={`tree-tab${leftTab === "notes" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("notes")}
                        title="all notes in this project"
                      >
                        notes
                      </button>
                    </>
                  ) : (
                    <span className="tree-tab tree-tab--active tree-tab--static">
                      {zoom === "platform" ? "services" : "filters"}
                    </span>
                  )}
                  <button
                    type="button"
                    className="tree-collapse"
                    onClick={() => setTreeCollapsed(true)}
                    title="collapse panel"
                    aria-label="collapse sidebar"
                  >
                    ‹
                  </button>
                </div>
                <div className="tree-body">
                  {zoom === "platform" ? (
                    <PlatformFilterPanel text={platformFilter} onChange={setPlatformFilter} />
                  ) : zoom === "service" ? (
                    <ServiceFilterPanel
                      view={serviceView}
                      filters={serviceFilters}
                      onChange={setServiceFilters}
                    />
                  ) : leftTab === "files" ? (
                    <FileTree onOpen={(id) => store.setSymbol(id)} />
                  ) : leftTab === "notes" ? (
                    <NotesList />
                  ) : platform && leftTab === "entrypoints" ? (
                    <EntrypointsPanel view={serviceView} onOpen={(id) => store.setSymbol(id)} />
                  ) : !rootFrame ? (
                    <p className="tree-placeholder">Pick a function to see its callers.</p>
                  ) : (
                    <CallersTree key={rootFrame.id} rootFrame={rootFrame} />
                  )}
                </div>
              </div>
            </>
          )}
        </aside>
        {!treeCollapsed && (
          <div
            className="resize-handle"
            onPointerDown={onResizeStart}
            role="separator"
            aria-orientation="vertical"
            title="drag to resize"
          />
        )}
        <div className="app-content">
          <SymbolPicker repo={serviceRepo} onPick={(s) => store.setSymbol(s)} />
          {platform && (
            <ZoomTrail
              level={zoom}
              hasPlatform={workspace}
              serviceName={serviceName}
              anchorTitle={trailAnchor}
              entrypointCount={entrypointCount}
              onZoom={setZoom}
            />
          )}
          {error && <div className="app-error">{error}</div>}
          {/* Only when there is nothing to look at. A reindex refetches the
              root frame, and announcing that over a view the reader is already
              reading makes a refresh look like a reload — the frame below
              stays put and is replaced when the new body arrives. */}
          {loading && !rootFrame && <div className="app-loading">loading…</div>}
          {zoom === "platform" ? (
            <>
              {/* Two readings of the same level. The graph is shaped by
                  service — who calls whom; the key index is shaped by the
                  thing they share, which is the question you have when you
                  know the event's name and not who is on it. */}
              <div className="platform-modes">
                <button
                  type="button"
                  className={`platform-mode${platformMode === "graph" ? " platform-mode--on" : ""}`}
                  onClick={() => setPlatformMode("graph")}
                >
                  services
                </button>
                <button
                  type="button"
                  className={`platform-mode${platformMode === "keys" ? " platform-mode--on" : ""}`}
                  onClick={() => setPlatformMode("keys")}
                >
                  keys
                </button>
              </div>
              {platformMode === "graph" ? (
                <PlatformView
                  anchor={rootFrame?.id ?? null}
                  filter={platformFilter}
                  selected={selectedService}
                  onSelect={setSelectedService}
                  onOpenService={store.openService}
                  // setSymbol already lands you in the code and clears the
                  // service pick, in one history entry.
                  onOpenSite={(id) => store.setSymbol(id)}
                />
              ) : (
                <ChannelIndex onSelectService={setSelectedService} />
              )}
            </>
          ) : zoom === "service" ? (
            <ServiceView
              view={serviceView}
              filters={serviceFilters}
              onProtoRootChanged={() => setProtoRevision((n) => n + 1)}
              onOpen={(id) => store.setSymbol(id)}
            />
          ) : (
            rootFrame && (
              <div className="app-root-frame">
                {/* Not keyed on the revision. Remounting made every expanded
                    child refetch, which is right, but it also unmounted them
                    first — so a reindex emptied the view and refilled it a few
                    seconds later. Each frame refetches its own children in
                    place instead, keeping what it has until the new body
                    arrives. */}
                <Frame
                  frame={rootFrame}
                  path={[]}
                  onZoomOut={platform ? () => setZoom("service") : undefined}
                />
                <StickyHeaders />
              </div>
            )
          )}
          {zoom === "frame" && !rootFrame && !loading && !error && (
            <p className="app-hint">
              Search for a function above and select one to start. Click any
              underlined call site to expand its body inline; interface calls
              surface a dropdown to pick which implementation to view. The
              call tree on the right mirrors what you expand — click a node to
              unfold it here and there at once. "▲ callers" in a frame header
              lists where that function is used; pick one to splice the caller
              above (the callers tab on the left walks whole chains toward
              entry points). Click a line number to start a selection,
              shift-click another to extend, then "fold" to collapse the
              range. URL hash carries your view — reload preserves it, and the
              link is shareable.
            </p>
          )}
        </div>
        {/* Everything this frame reaches, on the side it reaches it from:
            the call tree it expands into, and the platform edges that leave
            the service. What leads *to* the frame — its callers, the
            entrypoints, the files it lives among — stays on the left, so the
            two directions never share a panel.

            Frame level only. Above it the service columns already show both
            sides, so a third copy would just be stale, and there is no call
            tree to mirror. The outbounds tab additionally needs a recognized
            surface; without one it would have nothing to show and no honest
            way to say why. */}
        {zoom === "frame" && (
          <>
            {!rightIsCollapsed && (
              <div
                className="resize-handle"
                onPointerDown={(e) => onResizeStart(e, "right")}
                role="separator"
                aria-orientation="vertical"
                title="drag to resize"
              />
            )}
            <aside
              className={`right-panel${rightIsCollapsed ? " right-panel--collapsed" : ""}`}
              style={rightIsCollapsed ? undefined : { flex: `0 0 ${rightWidth}px` }}
            >
              {rightIsCollapsed ? (
                <button
                  type="button"
                  className="tree-expand"
                  onClick={() => setRightCollapsed(false)}
                  title="show the call tree and what this frame calls out to"
                  aria-label="show calls panel"
                >
                  <span className="tree-expand-icon">‹</span>
                  <span className="tree-expand-label">
                    {platform ? `outbounds${outboundCount ? ` · ${outboundCount}` : ""}` : "calls"}
                  </span>
                </button>
              ) : (
                <div className="tree-inner">
                  {/* Outbounds first: it's the platform-level answer, and the
                      default when there is one. */}
                  <div className="tree-header tree-tabs">
                    {platform && (
                      <button
                        type="button"
                        className={`tree-tab${
                          rightPanelTab === "outbounds" ? " tree-tab--active" : ""
                        }`}
                        onClick={() => setRightTab("outbounds")}
                        title="the calls this frame's code path makes out of the service"
                      >
                        outbounds
                        {outboundCount !== null && outboundCount > 0 && (
                          <span className="tree-tab-count">{outboundCount}</span>
                        )}
                      </button>
                    )}
                    <button
                      type="button"
                      className={`tree-tab${rightPanelTab === "calls" ? " tree-tab--active" : ""}`}
                      onClick={() => setRightTab("calls")}
                      title="what this frame expands into — click a node to unfold it here and in the code"
                    >
                      calls
                    </button>
                    <button
                      type="button"
                      className="tree-collapse"
                      onClick={() => setRightCollapsed(true)}
                      title="collapse panel"
                      aria-label="collapse calls panel"
                    >
                      ›
                    </button>
                  </div>
                  <div className="tree-body">
                    {platform && rightPanelTab === "outbounds" ? (
                      <OutboundsPanel view={serviceView} onOpen={(id) => store.setSymbol(id)} />
                    ) : rootFrame ? (
                      <CallTree rootFrame={rootFrame} />
                    ) : (
                      <p className="tree-placeholder">Pick a function to see its call tree.</p>
                    )}
                  </div>
                </div>
              )}
            </aside>
          </>
        )}
      </div>
    </div>
  );
}

// The saved-anchors list, shown atop the sidebar. Hidden when empty — the
// star in each frame header is how you add one.
//
// These are anchors, not just a reading list: the anchor is whatever frame is
// open, so clicking one here re-anchors every level at once — the entrypoints
// that reach it, the calls it makes, the lit rows at L1, the marked service
// at L0. The list is rendered from an array precisely so selecting several at
// once later is a change of arity, not of shape.
function BookmarksPanel({
  anchor,
  onOpen,
}: {
  // The live anchor, so the list can say which entry you're currently on
  // rather than looking like four equally-inactive links.
  anchor: string | null;
  onOpen: (id: string) => void;
}) {
  const { bookmarks, remove } = useBookmarks();
  if (bookmarks.length === 0) return null;
  return (
    <div className="bookmarks">
      <div className="bookmarks-header">
        <span className="bookmarks-title" title="saved anchors — click one to anchor on it">
          anchors
        </span>
        <span className="bookmarks-count">{bookmarks.length}</span>
      </div>
      <ul className="bookmarks-list">
        {bookmarks.map((b) => (
          <li
            key={b.targetId}
            className={`bookmark${b.targetId === anchor ? " bookmark--active" : ""}`}
          >
            <button
              type="button"
              className="bookmark-open"
              onClick={() => onOpen(b.targetId)}
              title={b.targetId}
              aria-current={b.targetId === anchor ? "true" : undefined}
            >
              <span className="bookmark-name">{b.title}</span>
              <span className="bookmark-loc">
                {shortFile(b.file)}
                {b.line ? `:${b.line}` : ""}
              </span>
            </button>
            <button
              type="button"
              className="bookmark-remove"
              onClick={() => remove(b.targetId)}
              title="remove bookmark"
              aria-label="remove bookmark"
            >
              ×
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function shortFile(p: string): string {
  const parts = p.split("/");
  return parts.slice(-2).join("/");
}

// Render a result label with the characters that matched the query bolded and
// the rest dimmed. Since a label is "Receiver.method" (Go FullName) and the
// backend already ranks method-name matches first, the bold span lands on the
// method when that's what matched, so the part you searched for stands out from
// the receiver/package noise. Matches every (case-insensitive) occurrence.
function highlightMatch(label: string, query: string): ReactNode {
  const q = query.trim().toLowerCase();
  if (!q) return label;
  const lower = label.toLowerCase();
  const parts: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < label.length) {
    const at = lower.indexOf(q, i);
    if (at === -1) {
      parts.push(
        <span key={key++} className="picker-dim">
          {label.slice(i)}
        </span>,
      );
      break;
    }
    if (at > i) {
      parts.push(
        <span key={key++} className="picker-dim">
          {label.slice(i, at)}
        </span>,
      );
    }
    parts.push(
      <b key={key++} className="picker-hit">
        {label.slice(at, at + q.length)}
      </b>,
    );
    i = at + q.length;
  }
  return parts;
}

// repo is the service being read, and it goes to the server with every query:
// which hits matter most depends on where you are, and above the frame level
// that need not be the repo unfold was launched in.
function SymbolPicker({
  repo,
  onPick,
}: {
  repo: string | null;
  onPick: (name: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [focused, setFocused] = useState(false);

  useEffect(() => {
    let alive = true;
    if (!query.trim()) {
      setResults([]);
      return;
    }
    setBusy(true);
    const handle = setTimeout(() => {
      search(query, 25, repo)
        .then((r) => {
          if (!alive) return;
          setResults(r);
          setBusy(false);
        })
        .catch(() => {
          if (alive) setBusy(false);
        });
    }, 120);
    return () => {
      alive = false;
      clearTimeout(handle);
    };
  }, [query, repo]);

  return (
    <div className="picker">
      <input
        type="text"
        placeholder="search functions… (e.g. main, Validate, Indexer.Load)"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (matches("search.openFirst", e) && results[0]) onPick(results[0].targetId);
        }}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        autoFocus
      />
      {busy && <span className="picker-busy">…</span>}
      {focused && results.length > 0 && (
        <ul className="picker-results">
          {results.map((r) => (
            <li key={r.targetId}>
              {/* preventDefault on mousedown keeps focus on the input so the
                  blur (which hides the dropdown) doesn't fire before this click
                  registers and the pick is lost. */}
              <button
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => onPick(r.targetId)}
                className="picker-pick"
              >
                <span className="picker-label">
                  {r.external && <span className="picker-dep">dep</span>}
                  {highlightMatch(r.label, query)}
                </span>
                <span className="picker-loc">
                  {r.file.split("/").slice(-2).join("/")}:{r.line}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
