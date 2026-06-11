import { useEffect, useState } from "react";
import { Frame } from "./Frame";
import { CallTree } from "./CallTree";
import { CallersTree } from "./CallersTree";
import { FileTree } from "./FileTree";
import { StickyHeaders } from "./StickyHeaders";
import { SettingsPanel } from "./SettingsPanel";
import { useSettings } from "./settings";
import { fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult } from "./types";
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
  const [sidebarTab, setSidebarTab] = useState<"files" | "calls" | "callers">("calls");
  const [reindexed, setReindexed] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settings = useSettings();
  const [sidebarWidth, setSidebarWidth] = useState(() => {
    const v = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return v >= SIDEBAR_MIN ? v : 280;
  });

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
      })
      .catch(() => {});
  }, []);

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
                <div className="tree-header tree-tabs">
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
                    className="tree-collapse"
                    onClick={() => setTreeCollapsed(true)}
                    title="collapse panel"
                    aria-label="collapse sidebar"
                  >
                    ‹
                  </button>
                </div>
                <div className="tree-body">
                  {sidebarTab === "files" ? (
                    <FileTree onOpen={(id) => store.setSymbol(id)} />
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
          {error && <div className="app-error">{error}</div>}
          {loading && <div className="app-loading">loading…</div>}
          {rootFrame && (
            <div className="app-root-frame">
              {/* Remount the whole frame tree on reindex so every expanded
                  child refetches; the expansion intent persists in the store. */}
              <Frame key={revision} frame={rootFrame} path={[]} />
              <StickyHeaders />
            </div>
          )}
          {!rootFrame && !loading && !error && (
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
          if (e.key === "Enter" && results[0]) onPick(results[0].targetId);
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
                <span className="picker-label">{r.label}</span>
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
