import { useEffect, useState } from "react";
import { Frame } from "./Frame";
import { fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult } from "./types";
import { ViewStoreProvider, useViewStore } from "./viewState";

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
      .then((h) => setTarget(h.target ?? null))
      .catch(() => {});
  }, []);

  return (
    <div className="app">
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
      </header>
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
          surface a dropdown to pick which implementation to view. Click a
          line number to start a selection, shift-click another to extend,
          then "fold" to collapse the range. URL hash carries your view —
          reload preserves it, and the link is shareable.
        </p>
      )}
    </div>
  );
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
