import { useEffect, useState } from "react";
import { Frame } from "./Frame";
import { CallTree } from "./CallTree";
import { fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult } from "./types";
import { ViewStoreProvider, useViewStore } from "./viewState";
import { setBookmarkProject, useBookmarks } from "./bookmarks";

const TREE_COLLAPSED_KEY = "unfold.tree.collapsed";

export function App() {
  return (
    <ViewStoreProvider>
      <AppShell />
    </ViewStoreProvider>
  );
}

function AppShell() {
  const store = useViewStore();
  const symbol = store.symbol;
  const [target, setTarget] = useState<string | null>(null);
  const [rootFrame, setRootFrame] = useState<FrameT | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [treeCollapsed, setTreeCollapsed] = useState(
    () => localStorage.getItem(TREE_COLLAPSED_KEY) === "1",
  );

  useEffect(() => {
    localStorage.setItem(TREE_COLLAPSED_KEY, treeCollapsed ? "1" : "0");
  }, [treeCollapsed]);

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
  }, [symbol]);

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
    <div className={`app${treeCollapsed ? " app--tree-collapsed" : ""}`}>
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
      </header>
      <div className="app-main">
        <aside className={`tree-panel${treeCollapsed ? " tree-panel--collapsed" : ""}`}>
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
              {rootFrame ? (
                <CallTree rootFrame={rootFrame} onCollapse={() => setTreeCollapsed(true)} />
              ) : (
                <div className="tree-inner">
                  <div className="tree-header">
                    <span className="tree-title">call tree</span>
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
                  <p className="tree-placeholder">Pick a function to see its call tree.</p>
                </div>
              )}
            </>
          )}
        </aside>
        <div className="app-content">
          <SymbolPicker onPick={(s) => store.setSymbol(s)} />
          {error && <div className="app-error">{error}</div>}
          {loading && <div className="app-loading">loading…</div>}
          {rootFrame && (
            <div className="app-root-frame">
              <Frame frame={rootFrame} path={[]} />
            </div>
          )}
          {!rootFrame && !loading && !error && (
            <p className="app-hint">
              Search for a function above and select one to start. Click any
              underlined call site to expand its body inline; interface calls
              surface a dropdown to pick which implementation to view. The
              call tree on the left mirrors what you expand — click a node to
              unfold it here and there at once. Click a line number to start a
              selection, shift-click another to extend, then "fold" to collapse
              the range. URL hash carries your view — reload preserves it, and
              the link is shareable.
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
        autoFocus
      />
      {busy && <span className="picker-busy">…</span>}
      {results.length > 0 && (
        <ul className="picker-results">
          {results.map((r) => (
            <li key={r.targetId}>
              <button onClick={() => onPick(r.targetId)} className="picker-pick">
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
