import { useEffect, useState, type ReactNode } from "react";
import { Frame } from "./Frame";
import { CallTree } from "./CallTree";
import { CallersTree } from "./CallersTree";
import { FileTree } from "./FileTree";
import { StickyHeaders } from "./StickyHeaders";
import { SettingsPanel } from "./SettingsPanel";
import { useSettings } from "./settings";
import { NotesList } from "./NotesUI";
import { loadNotes } from "./notes";
import { ServiceView } from "./ServiceView";
import { PlatformView } from "./PlatformView";
import { ZoomTrail } from "./ZoomTrail";
import {
  emptyServiceFilters,
  PlatformFilterPanel,
  ServiceFilterPanel,
  type ServiceFilters,
} from "./ZoomSidebar";
import { repoOf, zoomIn, zoomOut, type ZoomLevel } from "./zoom";
import { matches } from "./keybindings";
import { fetchServiceView, fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult, ServiceView as ServiceViewT } from "./types";
import { ViewStoreProvider, useViewStore } from "./viewState";
import { ReloadProvider, useReloadRevision } from "./reload";
import { setBookmarkProject, useBookmarks } from "./bookmarks";

const TREE_COLLAPSED_KEY = "unfold.tree.collapsed";
const SIDEBAR_WIDTH_KEY = "unfold.sidebar.width";
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
  const [sidebarTab, setSidebarTab] = useState<"files" | "calls" | "callers" | "notes">("calls");
  const [reindexed, setReindexed] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settings = useSettings();
  // Zoom is a level, not a tab: the frame tree stays mounted in the view
  // store either way, so switching levels never costs expansion state.
  const [zoom, setZoom] = useState<ZoomLevel>("frame");
  const [platform, setPlatform] = useState(false);
  const [workspace, setWorkspace] = useState(false);
  const [serviceName, setServiceName] = useState<string | null>(null);
  const [entrypointCount, setEntrypointCount] = useState<number | null>(null);
  const [trailAnchor, setTrailAnchor] = useState<string | null>(null);
  // Filters live here, not inside the views, because the sidebar owns them
  // once you're above the frame level.
  const [serviceFilters, setServiceFilters] = useState<ServiceFilters>(emptyServiceFilters);
  const [platformFilter, setPlatformFilter] = useState("");
  const [selectedService, setSelectedService] = useState<string | null>(null);
  const [loadedService, setLoadedService] = useState<ServiceViewT | null>(null);
  const [sidebarWidth, setSidebarWidth] = useState(() => {
    const v = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return v >= SIDEBAR_MIN ? v : 280;
  });

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

  // Drag the handle on the sidebar's right edge to resize it.
  function onResizeStart(e: React.PointerEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = sidebarWidth;
    const max = Math.max(280, Math.floor(window.innerWidth * 0.6));
    const move = (ev: PointerEvent) => {
      setSidebarWidth(Math.min(max, Math.max(SIDEBAR_MIN, startW + ev.clientX - startX)));
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

  useEffect(() => {
    fetch("/api/health")
      .then((r) => r.json())
      .then((h) => {
        setTarget(h.target ?? null);
        setBookmarkProject(h.target ?? null); // namespace bookmarks per project
        setPlatform(!!h.platform);
        setWorkspace(!!h.workspace);
      })
      .catch(() => {});
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
      .then((v) => {
        if (!alive) return;
        setServiceName(v.name);
        setTrailAnchor(v.anchorTitle ?? null);
        setEntrypointCount(
          v.anchor ? v.inbound.filter((b) => b.reachesAnchor).length : null,
        );
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [platform, rootFrame?.id, serviceRepo, revision]);

  // Keyboard zoom. The chords live in the keybinding registry, which is also
  // what the settings panel lists — so the documented shortcut and the wired
  // one can't disagree.
  useEffect(() => {
    if (!platform) return;
    function onKey(e: KeyboardEvent) {
      if (matches("zoom.out", e)) {
        e.preventDefault();
        setZoom((z) => zoomOut(z, workspace));
      } else if (matches("zoom.in", e)) {
        e.preventDefault();
        setZoom((z) => zoomIn(z, workspace));
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [platform, workspace]);

  // Loading a different symbol is a descent, so it lands you in the code —
  // and drops any explicit service pick, since the frame now decides which
  // service you're in.
  useEffect(() => {
    setZoom("frame");
    setSelectedService(null);
  }, [symbol]);



  return (
    <div
      className={`app${treeCollapsed ? " app--tree-collapsed" : ""}${
        settings.indentMode === "indent" ? " app--indent" : ""
      }`}
    >
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
        <button
          type="button"
          className={`app-settings${settingsOpen ? " app-settings--open" : ""}`}
          onClick={() => setSettingsOpen((v) => !v)}
          title="settings"
          aria-label="toggle settings"
        >
          ⚙
        </button>
      </header>
      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} />}
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
              <BookmarksPanel onOpen={(id) => store.setSymbol(id)} />
              <div className="tree-inner">
                {/* The sidebar follows the zoom level. Above the frame there
                    is no call tree to show, and what you need instead is a
                    way to cut a large surface down — so the tabs give way to
                    the filter panel rather than sitting there describing a
                    frame you're no longer looking at. */}
                <div className="tree-header tree-tabs">
                  {zoom === "frame" ? (
                    <>
                      <button
                        type="button"
                        className={`tree-tab${sidebarTab === "files" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("files")}
                      >
                        files
                      </button>
                      <button
                        type="button"
                        className={`tree-tab${sidebarTab === "calls" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("calls")}
                      >
                        calls
                      </button>
                      <button
                        type="button"
                        className={`tree-tab${sidebarTab === "callers" ? " tree-tab--active" : ""}`}
                        onClick={() => setSidebarTab("callers")}
                        title="who calls the focused function — expand to walk toward entry points"
                      >
                        callers
                      </button>
                      <button
                        type="button"
                        className={`tree-tab${sidebarTab === "notes" ? " tree-tab--active" : ""}`}
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
                      view={loadedService}
                      filters={serviceFilters}
                      onChange={setServiceFilters}
                    />
                  ) : sidebarTab === "files" ? (
                    <FileTree onOpen={(id) => store.setSymbol(id)} />
                  ) : sidebarTab === "notes" ? (
                    <NotesList />
                  ) : !rootFrame ? (
                    <p className="tree-placeholder">
                      Pick a function to see its {sidebarTab === "callers" ? "callers" : "call tree"}.
                    </p>
                  ) : sidebarTab === "callers" ? (
                    <CallersTree key={rootFrame.id} rootFrame={rootFrame} />
                  ) : (
                    <CallTree rootFrame={rootFrame} />
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
          <SymbolPicker onPick={(s) => store.setSymbol(s)} />
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
          {loading && <div className="app-loading">loading…</div>}
          {zoom === "platform" ? (
            <PlatformView
              anchor={rootFrame?.id ?? null}
              filter={platformFilter}
              selected={selectedService}
              onSelect={setSelectedService}
              onOpenService={(alias) => {
                setSelectedService(alias);
                setZoom("service");
              }}
              onOpenSite={(id) => {
                store.setSymbol(id);
                setZoom("frame");
              }}
            />
          ) : zoom === "service" ? (
            <ServiceView
              anchor={rootFrame?.id ?? null}
              repo={serviceRepo}
              filters={serviceFilters}
              onLoaded={setLoadedService}
              onOpen={(id) => {
                store.setSymbol(id);
                setZoom("frame");
              }}
            />
          ) : (
            rootFrame && (
              <div className="app-root-frame">
                {/* Remount the whole frame tree on reindex so every expanded
                    child refetches; the expansion intent persists in the store. */}
                <Frame
                  key={revision}
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
              call tree on the left mirrors what you expand — click a node to
              unfold it here and there at once. "▲ callers" in a frame header
              lists where that function is used; pick one to splice the caller
              above (the callers sidebar tab walks whole chains toward entry
              points). Click a line number to start a selection, shift-click
              another to extend, then "fold" to collapse the range. URL hash
              carries your view — reload preserves it, and the link is
              shareable.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

// The saved-symbols list, shown atop the sidebar. Hidden when empty — the
// star in each frame header is how you add one.
function BookmarksPanel({ onOpen }: { onOpen: (id: string) => void }) {
  const { bookmarks, remove } = useBookmarks();
  if (bookmarks.length === 0) return null;
  return (
    <div className="bookmarks">
      <div className="bookmarks-header">
        <span className="bookmarks-title">bookmarks</span>
        <span className="bookmarks-count">{bookmarks.length}</span>
      </div>
      <ul className="bookmarks-list">
        {bookmarks.map((b) => (
          <li key={b.targetId} className="bookmark">
            <button
              type="button"
              className="bookmark-open"
              onClick={() => onOpen(b.targetId)}
              title={b.targetId}
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

function SymbolPicker({ onPick }: { onPick: (name: string) => void }) {
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
      search(query, 25)
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
  }, [query]);

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
                <span className="picker-label">{highlightMatch(r.label, query)}</span>
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
